package org.modelfuzzng.tlc;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;
import tlc2.tool.Action;
import tlc2.tool.StateVec;
import tlc2.tool.TLCState;
import tlc2.tool.impl.FastTool;
import tlc2.tool.impl.Tool;
import util.SimpleFilenameToStream;

/** 比较 TLC 枚举动作与按需绑定动作在同一轨迹上的后继。 */
public final class LazyActionEquivalence {
    private LazyActionEquivalence() {}

    public static void main(String[] args) throws Exception {
        if (args.length != 3) {
            throw new IllegalArgumentException("usage: LazyActionEquivalence model eager.cfg lazy.cfg");
        }
        Path model = Path.of(args[0]).toAbsolutePath().normalize();
        Path eagerConfig = Path.of(args[1]).toAbsolutePath().normalize();
        Path lazyConfig = Path.of(args[2]).toAbsolutePath().normalize();
        FastTool eagerTool = tool(model, eagerConfig);
        FastTool lazyTool = tool(model, lazyConfig);
        RaftEventMapper mapper = new RaftEventMapper(
            lazyTool,
            lazyTool.getModule(withoutExtension(model.getFileName().toString(), ".tla")),
            ModelBounds.parse(Files.readString(lazyConfig, StandardCharsets.UTF_8)),
            32
        );

        TLCState eagerState = initial(eagerTool);
        TLCState lazyState = initial(lazyTool);
        assertSameState("Init", eagerState, lazyState);

        List<String> events = List.of(
            "{\"name\":\"Timeout\",\"params\":{\"node\":1}}",
            "{\"name\":\"DeliverMessage\",\"params\":{\"type\":\"MsgVote\",\"from\":1,\"to\":2,\"term\":1,\"log_term\":0,\"index\":0}}",
            "{\"name\":\"DeliverMessage\",\"params\":{\"type\":\"MsgVoteResp\",\"from\":2,\"to\":1,\"term\":1,\"reject\":false}}",
            "{\"name\":\"BecomeLeader\",\"params\":{\"node\":1}}",
            "{\"name\":\"ClientRequest\",\"params\":{\"leader\":1,\"request\":0}}"
        );
        for (String event : events) {
            Action lazyAction = mapper.map("[" + event + "]").get(0).action();
            Action eagerAction = enumeratedMatch(eagerTool.getActions(), lazyAction);
            eagerState = onlySuccessor(eagerTool, eagerAction, eagerState);
            lazyState = onlySuccessor(lazyTool, lazyAction, lazyState);
            assertSameState(lazyAction.getInvocationSignature(), eagerState, lazyState);
        }
        verifyBoundedCache(lazyTool, model, lazyConfig);
        System.out.println("lazy/eager action equivalence passed");
    }

    private static void verifyBoundedCache(FastTool tool, Path model, Path config) throws Exception {
        RaftEventMapper mapper = new RaftEventMapper(
            tool,
            tool.getModule(withoutExtension(model.getFileName().toString(), ".tla")),
            ModelBounds.parse(Files.readString(config, StandardCharsets.UTF_8)),
            1
        );
        mapper.map("[{\"name\":\"Timeout\",\"params\":{\"node\":1}}]");
        mapper.map("[{\"name\":\"Timeout\",\"params\":{\"node\":2}}]");
        mapper.map("[{\"name\":\"Timeout\",\"params\":{\"node\":1}}]");
        if (mapper.cachedActionCount() != 1
            || mapper.actionsCreatedCount() != 3
            || mapper.cacheEvictionCount() != 2) {
            throw new AssertionError("action cache is not bounded LRU");
        }
    }

    private static FastTool tool(Path model, Path config) {
        String modelName = withoutExtension(model.getFileName().toString(), ".tla");
        String configName = withoutExtension(config.getFileName().toString(), ".cfg");
        String[] searchPaths = {model.getParent().toString(), config.getParent().toString()};
        return new FastTool(
            modelName, configName, new SimpleFilenameToStream(searchPaths), Tool.Mode.Simulation, Map.of()
        );
    }

    private static TLCState initial(FastTool tool) throws Exception {
        StateVec states = tool.getInitStates();
        if (states.size() != 1) {
            throw new AssertionError("expected one initial state, got " + states.size());
        }
        TLCState state = states.elementAt(0);
        state.execCallable();
        state.deepNormalize();
        return state;
    }

    private static Action enumeratedMatch(Action[] actions, Action expected) {
        Action result = null;
        for (Action action : actions) {
            if (!action.getName().equals(expected.getName())
                || !action.getParameters().equals(expected.getParameters())) {
                continue;
            }
            if (result != null) {
                throw new AssertionError("ambiguous eager action " + expected.getInvocationSignature());
            }
            result = action;
        }
        if (result == null) {
            throw new AssertionError("missing eager action " + expected.getInvocationSignature());
        }
        return result;
    }

    private static TLCState onlySuccessor(FastTool tool, Action action, TLCState current) throws Exception {
        StateVec states = tool.getNextStates(action, current);
        if (states.size() != 1) {
            throw new AssertionError(action.getInvocationSignature() + " produced " + states.size() + " successors");
        }
        TLCState state = states.elementAt(0);
        state.execCallable();
        state.deepNormalize();
        return state;
    }

    private static void assertSameState(String step, TLCState eager, TLCState lazy) {
        if (!eager.toString().equals(lazy.toString())) {
            throw new AssertionError(step + " differs\neager:\n" + eager + "\nlazy:\n" + lazy);
        }
    }

    private static String withoutExtension(String value, String extension) {
        return value.endsWith(extension) ? value.substring(0, value.length() - extension.length()) : value;
    }
}
