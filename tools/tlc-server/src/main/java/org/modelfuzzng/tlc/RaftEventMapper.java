package org.modelfuzzng.tlc;

import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import tlc2.tool.Action;
import tlc2.value.impl.BoolValue;
import tlc2.value.impl.IntValue;
import tlc2.value.impl.Value;
import util.UniqueString;

/** 把 NG 当前模型事件协议映射为已经枚举参数的 TLA+ Action。 */
final class RaftEventMapper {
    private final Map<String, List<Action>> actionsByName = new HashMap<>();

    RaftEventMapper(Action[] actions) {
        for (Action action : actions) {
            actionsByName.computeIfAbsent(action.getName().toString(), ignored -> new ArrayList<>()).add(action);
        }
    }

    List<MappedEvent> map(String input) throws ProtocolException {
        final JsonElement parsed;
        try {
            parsed = JsonParser.parseString(input);
        } catch (RuntimeException error) {
            throw failure("invalid_json", -1, "", 400, error.getMessage());
        }
        if (!parsed.isJsonArray()) {
            throw failure("invalid_request", -1, "", 400, "request body must be a JSON array");
        }
        JsonArray array = parsed.getAsJsonArray();
        List<MappedEvent> result = new ArrayList<>(array.size());
        for (int index = 0; index < array.size(); index++) {
            if (!array.get(index).isJsonObject()) {
                throw failure("invalid_event", index, "", 400, "event must be a JSON object");
            }
            result.add(mapEvent(array.get(index).getAsJsonObject(), index));
        }
        return result;
    }

    private MappedEvent mapEvent(JsonObject event, int index) throws ProtocolException {
        if (optionalBoolean(event, "reset", false, index, "")) {
            return MappedEvent.resetEvent();
        }
        String name = requiredString(event, "name", index, "");
        JsonObject params = objectField(event, "params", index, name);
        Map<String, Object> expected = switch (name) {
            case "ClientRequest" -> values("i", number(params, "leader", index, name),
                "v", number(params, "request", index, name));
            case "BecomeLeader", "Timeout" -> values("i", number(params, "node", index, name));
            case "AdvanceCommitIndex", "Add", "Remove" -> values("i", number(params, "i", index, name));
            case "DeliverMessage" -> deliveredMessage(params, index, name);
            default -> throw failure("unknown_event", index, name, 400, "unsupported model event " + name);
        };
        String actionName = switch (name) {
            case "Add" -> "AddToActive";
            case "Remove" -> "RemoveFromActive";
            case "DeliverMessage" -> deliveredActionName(params, index, name);
            default -> name;
        };
        return MappedEvent.action(name, findAction(actionName, expected, index, name));
    }

    private String deliveredActionName(JsonObject params, int index, String eventName) throws ProtocolException {
        String type = requiredString(params, "type", index, eventName);
        return switch (type) {
            case "MsgVote" -> "HandleRequestVoteRequest";
            case "MsgVoteResp" -> "HandleRequestVoteResponse";
            case "MsgApp" -> entries(params, index, eventName).isEmpty()
                ? "HandleNilAppendEntriesRequest" : "HandleAppendEntriesRequest";
            case "MsgAppResp" -> "HandleAppendEntriesResponse";
            default -> throw failure("unknown_message_type", index, eventName, 400,
                "unsupported delivered message type " + type);
        };
    }

    private Map<String, Object> deliveredMessage(JsonObject params, int index, String eventName) throws ProtocolException {
        String type = requiredString(params, "type", index, eventName);
        long from = number(params, "from", index, eventName);
        long to = number(params, "to", index, eventName);
        long term = number(params, "term", index, eventName);
        return switch (type) {
            case "MsgVote" -> values(
                "i", to, "j", from, "lTerm", number(params, "log_term", index, eventName),
                "lIndex", number(params, "index", index, eventName), "term", term);
            case "MsgVoteResp" -> values(
                "i", to, "j", from, "term", term,
                "grant", !requiredBoolean(params, "reject", index, eventName));
            case "MsgApp" -> appendEntries(params, index, eventName, from, to, term);
            case "MsgAppResp" -> values(
                "i", to, "j", from, "term", term,
                "success", !requiredBoolean(params, "reject", index, eventName),
                "mIndex", number(params, "index", index, eventName));
            default -> throw failure("unknown_message_type", index, eventName, 400,
                "unsupported delivered message type " + type);
        };
    }

    private Map<String, Object> appendEntries(
        JsonObject params, int index, String eventName, long from, long to, long term
    ) throws ProtocolException {
        Map<String, Object> expected = values(
            "i", to, "j", from, "pLogIndex", number(params, "index", index, eventName),
            "pLogTerm", number(params, "log_term", index, eventName), "term", term,
            "cIndex", number(params, "commit", index, eventName));
        JsonArray entries = entries(params, index, eventName);
        if (entries.isEmpty()) {
            return expected;
        }
        if (entries.size() != 1 || !entries.get(0).isJsonObject()) {
            throw failure("invalid_entries", index, eventName, 400,
                "MsgApp model event must contain zero or one entry");
        }
        JsonObject entry = entries.get(0).getAsJsonObject();
        expected.put("entryTerm", number(entry, "Term", index, eventName));
        long value = 0;
        if (entry.has("Data")) {
            try {
                value = Long.parseLong(entry.get("Data").getAsString());
            } catch (RuntimeException error) {
                throw failure("invalid_entry_value", index, eventName, 400,
                    "entry Data must be a decimal model value");
            }
        }
        expected.put("entryValue", value);
        return expected;
    }

    private Action findAction(String actionName, Map<String, Object> expected, int index, String eventName)
        throws ProtocolException {
        List<Action> candidates = actionsByName.getOrDefault(actionName, List.of());
        Action match = null;
        for (Action candidate : candidates) {
            if (!parametersMatch(candidate, expected)) {
                continue;
            }
            if (match != null) {
                throw failure("ambiguous_action_mapping", index, eventName, 500,
                    "multiple TLA+ actions match " + actionName + expected);
            }
            match = candidate;
        }
        if (match == null) {
            throw failure("unmapped_action", index, eventName, 422,
                "no bounded TLA+ action matches " + actionName + expected);
        }
        return match;
    }

    private boolean parametersMatch(Action action, Map<String, Object> expected) {
        Map<UniqueString, Value> actual = action.getParameters();
        if (actual.size() != expected.size()) {
            return false;
        }
        for (Map.Entry<String, Object> item : expected.entrySet()) {
            Value value = actual.get(UniqueString.uniqueStringOf(item.getKey()));
            if (item.getValue() instanceof Boolean booleanValue) {
                if (!(value instanceof BoolValue actualBoolean) || actualBoolean.val != booleanValue) {
                    return false;
                }
            } else {
                long integer = (Long) item.getValue();
                if (!(value instanceof IntValue actualInteger) || actualInteger.val != integer) {
                    return false;
                }
            }
        }
        return true;
    }

    private static Map<String, Object> values(Object... items) {
        Map<String, Object> result = new HashMap<>();
        for (int index = 0; index < items.length; index += 2) {
            result.put((String) items[index], items[index + 1]);
        }
        return result;
    }

    private static JsonObject objectField(JsonObject object, String field, int index, String name)
        throws ProtocolException {
        if (!object.has(field) || !object.get(field).isJsonObject()) {
            throw failure("invalid_event", index, name, 400, field + " must be an object");
        }
        return object.getAsJsonObject(field);
    }

    private static JsonArray entries(JsonObject object, int index, String name) throws ProtocolException {
        if (!object.has("entries") || !object.get("entries").isJsonArray()) {
            throw failure("invalid_entries", index, name, 400, "entries must be an array");
        }
        return object.getAsJsonArray("entries");
    }

    private static String requiredString(JsonObject object, String field, int index, String name)
        throws ProtocolException {
        try {
            String value = object.get(field).getAsString();
            if (value.isBlank()) {
                throw new IllegalArgumentException("blank value");
            }
            return value;
        } catch (RuntimeException error) {
            throw failure("invalid_event", index, name, 400, field + " must be a non-empty string");
        }
    }

    private static long number(JsonObject object, String field, int index, String name) throws ProtocolException {
        try {
            return object.get(field).getAsLong();
        } catch (RuntimeException error) {
            throw failure("invalid_event", index, name, 400, field + " must be an integer");
        }
    }

    private static boolean requiredBoolean(JsonObject object, String field, int index, String name)
        throws ProtocolException {
        try {
            return object.get(field).getAsBoolean();
        } catch (RuntimeException error) {
            throw failure("invalid_event", index, name, 400, field + " must be boolean");
        }
    }

    private static boolean optionalBoolean(
        JsonObject object, String field, boolean fallback, int index, String name
    ) throws ProtocolException {
        if (!object.has(field)) {
            return fallback;
        }
        return requiredBoolean(object, field, index, name);
    }

    private static ProtocolException failure(
        String code, int index, String name, int status, String message
    ) {
        return new ProtocolException(code, index, name, status, message);
    }
}
