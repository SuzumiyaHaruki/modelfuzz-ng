package org.modelfuzzng.tlc;

import com.google.gson.Gson;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.Executors;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import tlc2.output.EC;
import tlc2.tool.Action;
import tlc2.tool.ITool;
import tlc2.tool.StateVec;
import tlc2.tool.TLCState;
import tlc2.tool.impl.FastTool;
import tlc2.tool.impl.Tool;
import tlc2.util.FP64;
import util.SimpleFilenameToStream;

/** 对 NG 模型事件执行严格、无跨请求状态的 controlled TLC 服务。 */
public final class StrictTLCServer {
    private static final String SERVER_VERSION = "2";
    private static final int VALIDATED_STATE_CACHE_SIZE = 100_000;
    private static final int DEFAULT_ACTION_CACHE_SIZE = 16_384;
    private static final Gson GSON = new Gson();

    private final ITool tool;
    private final RaftEventMapper mapper;
    private final String model;
    private final String config;
    private final Long maxLogIndex;
    private final Long largestTerm;
    private final TLCState initial;
    private final Action[] invariants;
    private final Map<Long, Boolean> validatedStates;
    private final ServerMetrics metrics = new ServerMetrics();

    private StrictTLCServer(String model, String config, int actionCacheSize) throws Exception {
        Path modelPath = Path.of(model).toAbsolutePath().normalize();
        Path configPath = Path.of(config).toAbsolutePath().normalize();
        String modelName = withoutExtension(modelPath.getFileName().toString(), ".tla");
        String configName = withoutExtension(configPath.getFileName().toString(), ".cfg");
        String[] searchPaths = {
            modelPath.getParent().toString(), configPath.getParent().toString()
        };
        FastTool fastTool = new FastTool(
            modelName, configName, new SimpleFilenameToStream(searchPaths), Tool.Mode.Simulation, Map.of()
        );
        this.tool = fastTool;
        this.model = modelPath.toString();
        this.config = configPath.toString();
        String configText = Files.readString(configPath, StandardCharsets.UTF_8);
        this.mapper = new RaftEventMapper(
            fastTool, fastTool.getModule(modelName), ModelBounds.parse(configText), actionCacheSize
        );
        this.maxLogIndex = constant(configText, "MaxLogIndex");
        this.largestTerm = constant(configText, "LargestTerm");
        FP64.Init(0);
        this.invariants = tool.getInvariants();
        this.validatedStates = new LinkedHashMap<>(16, 0.75f, true) {
            @Override
            protected boolean removeEldestEntry(Map.Entry<Long, Boolean> eldest) {
                return size() > VALIDATED_STATE_CACHE_SIZE;
            }
        };
        this.initial = loadInitialState();
    }

    public static void main(String[] args) throws Exception {
        Map<String, String> options = parseOptions(args);
        String model = requiredOption(options, "model");
        String config = requiredOption(options, "config");
        int port = Integer.parseInt(options.getOrDefault("port", "2023"));
        int actionCacheSize = Integer.parseInt(
            options.getOrDefault("action-cache-size", Integer.toString(DEFAULT_ACTION_CACHE_SIZE))
        );
        if (port < 1 || port > 65535) {
            throw new IllegalArgumentException("--port must be in 1..65535");
        }
        if (actionCacheSize < 1) {
            throw new IllegalArgumentException("--action-cache-size must be positive");
        }

        StrictTLCServer controlled = new StrictTLCServer(model, config, actionCacheSize);
        HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", port), 32);
        server.createContext("/health", controlled::health);
        server.createContext("/metrics", controlled::metrics);
        server.createContext("/execute", controlled::execute);
        server.setExecutor(Executors.newCachedThreadPool());
        server.start();
        System.out.printf("ModelFuzz-NG strict TLC server listening on 127.0.0.1:%d model=%s%n", port, controlled.model);
    }

    private void health(HttpExchange exchange) throws IOException {
        if (!exchange.getRequestMethod().equalsIgnoreCase("GET")) {
            send(exchange, 405, Map.of("error", Map.of("code", "method_not_allowed")));
            return;
        }
        Map<String, Object> response = new LinkedHashMap<>();
        response.put("status", "ok");
        response.put("server", "modelfuzz-ng-tlc");
        response.put("version", SERVER_VERSION);
        response.put("strict", true);
        response.put("model", model);
        response.put("config", config);
        if (maxLogIndex != null) {
            response.put("max_log_index", maxLogIndex);
        }
        if (largestTerm != null) {
            response.put("largest_term", largestTerm);
        }
        response.put("validated_state_cache_limit", VALIDATED_STATE_CACHE_SIZE);
        response.put("action_mode", "lazy");
        response.put("action_definitions", mapper.actionDefinitionCount());
        response.put("cached_actions", mapper.cachedActionCount());
        response.put("action_cache_limit", mapper.actionCacheLimit());
        response.put("metrics", metrics.snapshot(mapper));
        send(exchange, 200, response);
    }

    private void metrics(HttpExchange exchange) throws IOException {
        if (!exchange.getRequestMethod().equalsIgnoreCase("GET")) {
            send(exchange, 405, Map.of("error", Map.of("code", "method_not_allowed")));
            return;
        }
        send(exchange, 200, metrics.snapshot(mapper));
    }

    private void execute(HttpExchange exchange) throws IOException {
        if (!exchange.getRequestMethod().equalsIgnoreCase("POST")) {
            send(exchange, 405, Map.of("error", Map.of("code", "method_not_allowed")));
            return;
        }
        metrics.request();
        try {
            String input = new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8);
            Map<String, Object> result = simulate(input);
            metrics.success();
            send(exchange, 200, result);
        } catch (ProtocolException error) {
            metrics.error(error.code());
            Map<String, Object> detail = new LinkedHashMap<>();
            detail.put("code", error.code());
            detail.put("event_index", error.eventIndex());
            detail.put("event_name", error.eventName());
            detail.put("message", error.getMessage());
            send(exchange, error.httpStatus(), Map.of("error", detail));
        } catch (Exception error) {
            metrics.error("internal_error");
            send(exchange, 500, Map.of("error", Map.of(
                "code", "internal_error", "message", String.valueOf(error.getMessage())
            )));
        }
    }

    private synchronized Map<String, Object> simulate(String input) throws Exception {
        long mappingStarted = System.nanoTime();
        List<MappedEvent> events;
        try {
            events = mapper.map(input);
        } finally {
            metrics.mapping(System.nanoTime() - mappingStarted);
        }
        metrics.events((int) events.stream().filter(event -> !event.reset()).count());
        TLCState current = initial.deepCopy();
        List<TLCState> visited = new ArrayList<>();
        visited.add(current);
        boolean resetSeen = false;

        for (int index = 0; index < events.size(); index++) {
            MappedEvent event = events.get(index);
            if (event.reset()) {
                if (index != events.size() - 1) {
                    throw new ProtocolException("events_after_reset", index, event.externalName(), 400,
                        "reset must be the final event");
                }
                resetSeen = true;
                continue;
            }
            long successorStarted = System.nanoTime();
            StateVec successors;
            try {
                successors = tool.getNextStates(event.action(), current);
            } finally {
                metrics.successor(System.nanoTime() - successorStarted);
            }
            if (successors.empty()) {
                throw new ProtocolException("disabled_action", index, event.externalName(), 422,
                    "mapped TLA+ action is disabled in the current model state");
            }
            if (successors.size() != 1) {
                throw new ProtocolException("ambiguous_successor", index, event.externalName(), 409,
                    "mapped TLA+ action produced " + successors.size() + " successors");
            }
            TLCState next = successors.elementAt(0);
            if (next == null) {
                throw new ProtocolException("null_successor", index, event.externalName(), 500,
                    "TLC returned a null successor");
            }
            next.execCallable();
            next.deepNormalize();
            long validationStarted = System.nanoTime();
            try {
                validateState(next, index, event.externalName());
            } finally {
                metrics.validation(System.nanoTime() - validationStarted);
            }
            current = next;
            visited.add(current);
        }
        if (!resetSeen) {
            throw new ProtocolException("missing_reset", events.size(), "reset", 400,
                "request must end with a reset event");
        }

        List<String> states = new ArrayList<>(visited.size());
        List<Long> keys = new ArrayList<>(visited.size());
        long serializationStarted = System.nanoTime();
        try {
            for (TLCState state : visited) {
                states.add(state.toString());
                keys.add(state.fingerPrint());
            }
        } finally {
            metrics.serialization(System.nanoTime() - serializationStarted);
        }
        Map<String, Object> response = new LinkedHashMap<>();
        response.put("States", states);
        response.put("Keys", keys);
        return response;
    }

    private TLCState loadInitialState() throws Exception {
        int assumptionResult = tool.checkAssumptions();
        if (assumptionResult != EC.NO_ERROR) {
            throw new IllegalStateException("TLC assumption check failed with code " + assumptionResult);
        }
        StateVec initial = tool.getInitStates();
        if (initial.size() != 1) {
            throw new IllegalStateException("model must have exactly one initial state, got " + initial.size());
        }
        TLCState state = initial.elementAt(0);
        state.execCallable();
        state.deepNormalize();
        validateState(state, -1, "Init");
        return state;
    }

    private void validateState(TLCState state, int index, String eventName) throws ProtocolException {
        if (!tool.isGoodState(state)) {
            throw new ProtocolException("incomplete_state", index, eventName, 422,
                "TLC state does not assign every model variable");
        }
        long fingerprint = state.fingerPrint();
        if (validatedStates.containsKey(fingerprint)) {
            return;
        }
        try {
            if (!tool.isInModel(state)) {
                throw new ProtocolException("model_constraint_violation", index, eventName, 422,
                    "TLC state violates the model constraint");
            }
        } catch (ProtocolException error) {
            throw error;
        } catch (Exception error) {
            throw new ProtocolException("model_constraint_error", index, eventName, 500, error.getMessage());
        }
        for (Action invariant : invariants) {
            if (!tool.isValid(invariant, state)) {
                throw new ProtocolException("invariant_violation", index, eventName, 422,
                    "state violates invariant " + invariant.getName());
            }
        }
        validatedStates.put(fingerprint, Boolean.TRUE);
    }

    private static void send(HttpExchange exchange, int status, Object body) throws IOException {
        byte[] encoded = GSON.toJson(body).getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json; charset=utf-8");
        exchange.sendResponseHeaders(status, encoded.length);
        exchange.getResponseBody().write(encoded);
        exchange.close();
    }

    private static Map<String, String> parseOptions(String[] args) {
        Map<String, String> result = new HashMap<>();
        for (int index = 0; index < args.length; index += 2) {
            if (!args[index].startsWith("--") || index + 1 >= args.length) {
                throw new IllegalArgumentException("arguments must be --name value pairs");
            }
            result.put(args[index].substring(2), args[index + 1]);
        }
        return result;
    }

    private static String requiredOption(Map<String, String> options, String name) {
        String value = options.get(name);
        if (value == null || value.isBlank()) {
            throw new IllegalArgumentException("missing --" + name);
        }
        return value;
    }

    private static String withoutExtension(String value, String extension) {
        return value.endsWith(extension) ? value.substring(0, value.length() - extension.length()) : value;
    }

    private static Long constant(String configText, String name) {
        Matcher matcher = Pattern.compile("(?m)^\\s*" + Pattern.quote(name) + "\\s*=\\s*(\\d+)\\s*$")
            .matcher(configText);
        return matcher.find() ? Long.parseLong(matcher.group(1)) : null;
    }
}
