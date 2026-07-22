package org.modelfuzzng.tlc;

import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.LongAdder;
import tla2sany.semantic.FormalParamNode;
import tla2sany.semantic.ModuleNode;
import tla2sany.semantic.OpDefNode;
import tlc2.tool.Action;
import tlc2.tool.ITool;
import tlc2.util.Context;
import tlc2.value.impl.BoolValue;
import tlc2.value.impl.IntValue;

/** 把 NG 当前模型事件协议按需绑定为 TLA+ Action。 */
final class RaftEventMapper {
    private final ITool tool;
    private final Map<String, ActionDefinition> definitionsByName = new HashMap<>();
    private final Map<ActionKey, Action> actionCache;
    private final ModelBounds bounds;
    private final boolean storageSnapshotProfile;
    private final int actionCacheLimit;
    private final LongAdder lookupCount = new LongAdder();
    private final LongAdder lookupNanos = new LongAdder();
    private final LongAdder cacheHits = new LongAdder();
    private final LongAdder cacheMisses = new LongAdder();
    private final LongAdder actionsCreated = new LongAdder();
    private final LongAdder cacheEvictions = new LongAdder();

    RaftEventMapper(ITool tool, ModuleNode module, ModelBounds bounds, int actionCacheLimit) {
        if (actionCacheLimit < 1) {
            throw new IllegalArgumentException("action cache limit must be positive");
        }
        this.tool = tool;
        this.bounds = bounds;
        this.actionCacheLimit = actionCacheLimit;
        for (String name : List.of(
            "RemoveFromActive", "AddToActive", "Timeout", "BecomeLeader", "ClientRequest",
            "HandleRequestVoteRequest", "HandleRequestVoteResponse", "HandleNilAppendEntriesRequest",
            "HandleAppendEntriesRequest", "HandleAppendEntriesResponse", "AdvanceCommitIndex",
            "StorageRemoveFromActive", "StorageAddToActive", "StorageTimeout", "StorageBecomeLeader",
            "StorageClientRequest", "StorageHandleRequestVoteRequest", "StorageHandleRequestVoteResponse",
            "StorageHandleNilAppendEntriesRequest", "StorageHandleAppendEntriesRequest",
            "StorageHandleAppendEntriesResponse", "StorageAdvanceCommitIndex",
            "ApplyCommitted", "CreateSnapshot", "CompactLog", "SendSnapshot",
            "InstallSnapshot", "FastForwardSnapshot", "RejectSnapshot", "HandleSnapshotStatus"
        )) {
            OpDefNode definition = module.getOpDef(name);
            if (definition != null) {
                definitionsByName.put(name, new ActionDefinition(definition));
            }
        }
        this.storageSnapshotProfile = definitionsByName.containsKey("StorageTimeout")
            && definitionsByName.containsKey("CreateSnapshot");
        this.actionCache = new LinkedHashMap<>(16, 0.75f, true) {
            @Override
            protected boolean removeEldestEntry(Map.Entry<ActionKey, Action> eldest) {
                boolean remove = size() > RaftEventMapper.this.actionCacheLimit;
                if (remove) {
                    cacheEvictions.increment();
                }
                return remove;
            }
        };
    }

    int actionDefinitionCount() {
        return definitionsByName.size();
    }

    synchronized int cachedActionCount() {
        return actionCache.size();
    }

    int actionCacheLimit() {
        return actionCacheLimit;
    }

    String modelProfile() {
        return storageSnapshotProfile ? "storage-snapshot" : "basic";
    }

    long lookupCount() {
        return lookupCount.sum();
    }

    long lookupNanos() {
        return lookupNanos.sum();
    }

    long cacheHitCount() {
        return cacheHits.sum();
    }

    long cacheMissCount() {
        return cacheMisses.sum();
    }

    long actionsCreatedCount() {
        return actionsCreated.sum();
    }

    long cacheEvictionCount() {
        return cacheEvictions.sum();
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
            case "ApplyCommitted", "CompactLog" -> values(
                "i", number(params, "i", index, name),
                "index", number(params, "index", index, name));
            case "CreateSnapshot" -> values(
                "i", number(params, "i", index, name),
                "index", number(params, "index", index, name),
                "term", number(params, "term", index, name));
            case "SendSnapshot" -> values(
                "i", number(params, "i", index, name),
                "j", number(params, "j", index, name),
                "index", number(params, "index", index, name),
                "term", number(params, "term", index, name),
                "match", number(params, "match", index, name),
                "next", number(params, "next", index, name),
                "pending", number(params, "pending", index, name));
            case "InstallSnapshot", "FastForwardSnapshot", "RejectSnapshot" -> values(
                "i", number(params, "i", index, name),
                "j", number(params, "j", index, name),
                "index", number(params, "index", index, name),
                "sTerm", number(params, "snapshot_term", index, name),
                "term", number(params, "term", index, name));
            case "HandleSnapshotStatus" -> values(
                "i", number(params, "i", index, name),
                "j", number(params, "j", index, name),
                "success", requiredBoolean(params, "success", index, name),
                "next", number(params, "next", index, name));
            case "DeliverMessage" -> deliveredMessage(params, index, name);
            default -> throw failure("unknown_event", index, name, 400, "unsupported model event " + name);
        };
        String actionName = switch (name) {
            case "Add" -> "AddToActive";
            case "Remove" -> "RemoveFromActive";
            case "DeliverMessage" -> deliveredActionName(params, index, name);
            default -> name;
        };
        if (storageSnapshotProfile && isBaseRaftAction(actionName)) {
            actionName = "Storage" + actionName;
        }
        return MappedEvent.action(name, findAction(actionName, expected, index, name));
    }

    private static boolean isBaseRaftAction(String name) {
        return switch (name) {
            case "RemoveFromActive", "AddToActive", "Timeout", "BecomeLeader", "ClientRequest",
                "HandleRequestVoteRequest", "HandleRequestVoteResponse", "HandleNilAppendEntriesRequest",
                "HandleAppendEntriesRequest", "HandleAppendEntriesResponse", "AdvanceCommitIndex" -> true;
            default -> false;
        };
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

    private synchronized Action findAction(String actionName, Map<String, Object> expected, int index, String eventName)
        throws ProtocolException {
        long started = System.nanoTime();
        lookupCount.increment();
        try {
            ActionDefinition definition = definitionsByName.get(actionName);
            if (definition == null) {
                throw failure("unmapped_action", index, eventName, 422,
                    "no bounded TLA+ action matches " + actionName + expected);
            }
            ParameterTuple key = definition.keyFor(expected, bounds);
            if (key == null) {
                throw failure("unmapped_action", index, eventName, 422,
                    "no bounded TLA+ action matches " + actionName + expected);
            }
            ActionKey cacheKey = new ActionKey(actionName, key);
            Action cached = actionCache.get(cacheKey);
            if (cached != null) {
                cacheHits.increment();
                return cached;
            }
            cacheMisses.increment();
            Action created = definition.create(tool, expected);
            actionsCreated.increment();
            actionCache.put(cacheKey, created);
            return created;
        } finally {
            lookupNanos.add(System.nanoTime() - started);
        }
    }

    /** 从操作符形参建立 schema，事件到达前不创建任何具体 Action。 */
    private static final class ActionDefinition {
        private final OpDefNode definition;
        private final FormalParamNode[] parameters;

        ActionDefinition(OpDefNode definition) {
            this.definition = definition;
            this.parameters = definition.getParams();
        }

        ParameterTuple keyFor(Map<String, Object> expected, ModelBounds bounds) {
            if (expected.size() != parameters.length) {
                return null;
            }
            long[] values = new long[parameters.length];
            for (int index = 0; index < parameters.length; index++) {
                String name = parameters[index].getName().toString();
                if (!expected.containsKey(name)) {
                    return null;
                }
                Object value = expected.get(name);
                if (value instanceof Boolean booleanValue) {
                    values[index] = booleanValue ? 1 : 0;
                } else if (value instanceof Long integerValue
                    && integerValue >= Integer.MIN_VALUE && integerValue <= Integer.MAX_VALUE
                    && bounds.contains(name, integerValue)) {
                    values[index] = integerValue;
                } else {
                    return null;
                }
            }
            return new ParameterTuple(values);
        }

        Action create(ITool tool, Map<String, Object> expected) {
            Context context = Context.Empty;
            for (FormalParamNode parameter : parameters) {
                Object value = expected.get(parameter.getName().toString());
                if (value instanceof Boolean booleanValue) {
                    context = context.cons(parameter, booleanValue ? BoolValue.ValTrue : BoolValue.ValFalse);
                } else {
                    context = context.cons(parameter, IntValue.gen(Math.toIntExact((Long) value)));
                }
            }
            return new Action(tool, definition.getBody(), context, definition);
        }
    }

    private record ActionKey(String actionName, ParameterTuple parameters) {}

    /** long[] 需要内容相等语义，不能直接使用数组自身的 identity equals。 */
    private static final class ParameterTuple {
        private final long[] values;
        private final int hashCode;

        ParameterTuple(long[] values) {
            this.values = values;
            this.hashCode = Arrays.hashCode(values);
        }

        @Override
        public boolean equals(Object other) {
            return other instanceof ParameterTuple tuple && Arrays.equals(values, tuple.values);
        }

        @Override
        public int hashCode() {
            return hashCode;
        }
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
