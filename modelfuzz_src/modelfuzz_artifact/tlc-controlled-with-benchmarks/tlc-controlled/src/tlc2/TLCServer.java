package tlc2;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.IdentityHashMap;
import java.util.List;
import java.util.Queue;

import com.google.gson.Gson;
import com.sun.net.httpserver.*;

import tlc2.controlled.protocol.ActionMapper;
import tlc2.controlled.protocol.ActionMapperFactory;
import tlc2.controlled.protocol.ActionWrapper;
import tlc2.controlled.protocol.MappedAction;
import tlc2.controlled.protocol.StateAbstractor;
import tlc2.controlled.protocol.StateAbstractorFactory;
import tlc2.output.EC;
import tlc2.output.MP;
import tlc2.tool.Action;
import tlc2.tool.ITool;
import tlc2.tool.StateVec;
import tlc2.tool.TLCState;
import tlc2.tool.impl.FastTool;
import tlc2.tool.impl.Tool;
import tlc2.value.impl.CounterExample;
import tlc2.util.RandomGenerator;
import tlc2.util.FP64;
import util.FileUtil;
import util.SimpleFilenameToStream;

public class TLCServer extends TLC {

    private ITool tool;
    private ActionMapper mapper;
    private StateAbstractor abstractor;

    public TLCServer() {
        super();
        // Controlled executions use fingerprints as persistent coverage keys.
        // TLC otherwise chooses a random polynomial for every JVM process.
        this.fpIndex = 0;
    }

    public void init() {
        this.tool = new FastTool(mainFile, configFile, resolver, Tool.Mode.Simulation, params);
        this.mapper = ActionMapperFactory.getMapper(this.mapperParams, Arrays.asList(this.tool.getActions()), this.tool.getRootName());
        this.abstractor = StateAbstractorFactory.getStateAbstractor(this.mapperParams);
        FP64.Init(fpIndex);
    }

    public SimulationResult simulate(String input) throws Exception{
        
        StateVec initStates = computeInitStates(this.tool);
			List<TLCState> statesVisited = new ArrayList<TLCState>();
			List<SimulationTransition> transitions = new ArrayList<SimulationTransition>();
			IdentityHashMap<TLCState, Integer> stateEventIndices = new IdentityHashMap<TLCState, Integer>();

        StateVec nextStates = new StateVec(1);
        TLCState curState = randomState(initStates);

        TLCState initialState = snapshotState(curState);
        statesVisited.add(initialState);
        stateEventIndices.put(initialState, -1);
        List<MappedAction> actionsToRun = this.mapper.mapListOfActionsWithProvenance(input);
        for (MappedAction mappedAction : actionsToRun) {
            ActionWrapper nextAction = mappedAction.getAction();
            if (mappedAction.isIgnored()) {
                TLCState snapshot = snapshotState(curState);
                transitions.add(SimulationTransition.ignored(
                    mappedAction.getInputIndex(), mappedAction.getInputName(), snapshot));
                continue;
            }
            if(nextAction.isReset() || nextAction.isQuit() || nextAction.action.equals(Action.UNKNOWN)) {
                break;
            }

            nextStates.clear();
            TLCState preState = snapshotState(curState);
            nextStates = nextStates.addElements(tool.getNextStates(nextAction.action, curState));
            if(nextStates.empty()) {
                TLCState snapshot = snapshotState(curState);
                statesVisited.add(snapshot);
                stateEventIndices.put(snapshot, mappedAction.getInputIndex());
                transitions.add(SimulationTransition.disabled(
                    mappedAction.getInputIndex(), mappedAction.getInputName(),
                    nextAction.action.getName().toString(), snapshot));
                continue;
            }
            assert(nextStates.size() == 1);
            final TLCState s1 = nextStates.elementAt(0);
            s1.execCallable();
            curState = s1;
            TLCState postState = snapshotState(curState);
            statesVisited.add(postState);
            stateEventIndices.put(postState, mappedAction.getInputIndex());
            transitions.add(SimulationTransition.executed(
                mappedAction.getInputIndex(), mappedAction.getInputName(),
                nextAction.action.getName().toString(), preState, postState));
        }
        List<TLCState> abstractStates = this.abstractor.doAbstraction(statesVisited);
        List<Integer> abstractStateEventIndices = new ArrayList<Integer>(abstractStates.size());
        for (TLCState state : abstractStates) {
            Integer eventIndex = stateEventIndices.get(state);
            abstractStateEventIndices.add(eventIndex == null ? -2 : eventIndex);
        }
        return new SimulationResult(abstractStates, transitions, abstractStateEventIndices);
    }

    private TLCState snapshotState(TLCState state) {
        return state.deepCopy();
    }

    private TLCState randomState(StateVec states) {
        RandomGenerator gen = new RandomGenerator();
        final int len = states.size();
		if (len > 0) {
			final int index = (int) Math.floor(gen.nextDouble() * len);
			return states.elementAt(index);
		}
		return null;
    }

    public StateVec computeInitStates(ITool tool) throws Exception{
        final int res = tool.checkAssumptions();
		if (res != EC.NO_ERROR) {
			throw new Exception("Error checking assumptions: "+res);
		}
		
		TLCState curState = null;
        StateVec initStates;

		//
		// Compute the initial states.
		//
		try {
			// The init states are calculated only ever once and never change
			// in the loops below. Ideally the variable would be final.
			final StateVec inits = tool.getInitStates();
			initStates = new StateVec(inits.size());
			
            Action[] invariants = tool.getInvariants();
			// Check all initial states for validity.
			for (int i = 0; i < inits.size(); i++) {
				curState = inits.elementAt(i);
				if (tool.isGoodState(curState)) {
					for (int j = 0; j < invariants.length; j++) {
						if (!tool.isValid(invariants[j], curState)) {
							// We get here because of invariant violation.
                            String errorMessage = MP.getError(EC.TLC_INVARIANT_VIOLATED_INITIAL,
									new String[] { tool.getInvNames()[j], tool.evalAlias(curState, curState).toString() });
							tool.checkPostConditionWithCounterExample(new CounterExample(curState));
                            throw new Exception(errorMessage);
							
						}
					}
				} else {
					throw new Exception(MP.getError(EC.TLC_STATE_NOT_COMPLETELY_SPECIFIED_INITIAL, curState.toString()));
				}
				
				if (tool.isInModel(curState)) {
					initStates.addElement(curState);
				}
			}
		} catch (Exception e) {
            
			final String errorMessage;
			if (curState != null) {
				errorMessage = MP.getError(EC.TLC_INITIAL_STATE,
						new String[] { (e.getMessage() == null) ? e.toString() : e.getMessage(), curState.toString() });
			} else {
				errorMessage = e.getMessage(); // LL changed call 7 April 2012
			}
			throw new Exception(errorMessage);
		}

		// It appears deepNormalize brings the states into a canonical form to
		// speed up equality checks.
		initStates.deepNormalize();
        return initStates;
    }

    public static class SimulationTransition {
        public final int eventIndex;
        public final String inputName;
        public final String mappedAction;
        public final String status;
        public final long preKey;
        public final long postKey;

        private SimulationTransition(int eventIndex, String inputName, String mappedAction,
                String status, TLCState preState, TLCState postState) {
            this.eventIndex = eventIndex;
            this.inputName = inputName;
            this.mappedAction = mappedAction;
            this.status = status;
            // Capture immutable values now. Reading TLCState references during
            // response serialization lets later suffix actions rewrite history.
            this.preKey = preState.fingerPrint();
            this.postKey = postState.fingerPrint();
        }

        public static SimulationTransition ignored(int eventIndex, String inputName, TLCState state) {
            return new SimulationTransition(eventIndex, inputName, null, "ignored", state, state);
        }

        public static SimulationTransition disabled(int eventIndex, String inputName,
                String mappedAction, TLCState state) {
            return new SimulationTransition(eventIndex, inputName, mappedAction, "disabled", state, state);
        }

        public static SimulationTransition executed(int eventIndex, String inputName,
                String mappedAction, TLCState preState, TLCState postState) {
            return new SimulationTransition(
                eventIndex, inputName, mappedAction, "executed", preState, postState);
        }
    }

    public static class SimulationResult {
        public final List<TLCState> states;
        public final List<SimulationTransition> transitions;
        public final List<Integer> stateEventIndices;

        public SimulationResult(List<TLCState> states, List<SimulationTransition> transitions,
                List<Integer> stateEventIndices) {
            this.states = states;
            this.transitions = transitions;
            this.stateEventIndices = stateEventIndices;
        }
    }

    public static class TransitionResponse {
        public int eventIndex;
        public String inputName;
        public String mappedAction;
        public String status;
        public long preKey;
        public long postKey;

        public TransitionResponse(SimulationTransition transition) {
            this.eventIndex = transition.eventIndex;
            this.inputName = transition.inputName;
            this.mappedAction = transition.mappedAction;
            this.status = transition.status;
            this.preKey = transition.preKey;
            this.postKey = transition.postKey;
        }
    }

    public static class ServerResponse {
        public List<String> states;

        public List<Long> keys;

        public List<TransitionResponse> transitions;

        public List<Integer> stateEventIndices;

        public ServerResponse(List<String> states, List<Long> keys,
                List<TransitionResponse> transitions, List<Integer> stateEventIndices) {
            this.states = states;
            this.keys = keys;
            this.transitions = transitions;
            this.stateEventIndices = stateEventIndices;
        }
    }

    public static void main(String[] args) throws Exception {
        System.out.println("Initializing...");
        final TLCServer tlcServer = new TLCServer();
        if (!tlcServer.handleParameters(args)) {
            System.exit(1);
        }
        final String dir = FileUtil.parseDirname(tlcServer.getMainFile());
        if (!dir.isEmpty()) {
            tlcServer.setResolver(new SimpleFilenameToStream(dir));
        } else {
            tlcServer.setResolver(new SimpleFilenameToStream());
        }
        tlcServer.init();

        int serverPort = 2023;
        if (tlcServer.mapperParams.containsKey("port"))
            serverPort = Integer.parseInt(tlcServer.mapperParams.get("port"));

        int index = 0;
		while (index < args.length) {
            if (args[index].equals("-serverport")) {
                index++;
                if (index < args.length) {
                    try {
                        serverPort = Integer.parseInt(args[index]);
                    } catch (NumberFormatException e) {
                        MP.printError(EC.WRONG_COMMANDLINE_PARAMS_TLC, "server port should be a number");
                        System.exit(1);;
                    }
                }
            }
            index++;
        }
        try {
            HttpServer httpServer = HttpServer.create(new InetSocketAddress(serverPort), 0);
            httpServer.createContext("/health", new HttpHandler() {
                public void handle(HttpExchange t) throws IOException {
                    byte[] response = "Ok".getBytes(StandardCharsets.UTF_8);
                    t.sendResponseHeaders(200, response.length);
                    OutputStream responseStream = t.getResponseBody();
                    responseStream.write(response);
                    responseStream.close();
                }
            });
            httpServer.createContext("/execute", new HttpHandler() {
                public void handle(HttpExchange t) throws IOException {
                    if(!t.getRequestMethod().equalsIgnoreCase("POST")) {
                        t.sendResponseHeaders(405, -1);
                        return;
                    }
                    try {
                        byte[] requestBytes = t.getRequestBody().readAllBytes();
                        String request = new String(requestBytes, StandardCharsets.UTF_8);
                        SimulationResult execution = tlcServer.simulate(request);
                        List<String> stringTrace = new ArrayList<>();
                        List<Long> fingerprintTrace = new ArrayList<>();
                        for( TLCState state : execution.states) {
                            stringTrace.add(state.toString());
                            fingerprintTrace.add(state.fingerPrint());
                        }
                        List<TransitionResponse> transitionTrace = new ArrayList<>();
                        for (SimulationTransition transition : execution.transitions) {
                            transitionTrace.add(new TransitionResponse(transition));
                        }
                        Gson gson = new Gson();
                        String response = gson.toJson(new ServerResponse(
                            stringTrace,
                            fingerprintTrace,
                            transitionTrace,
                            execution.stateEventIndices));
                        t.sendResponseHeaders(200, response.length());
                        OutputStream responseStream = t.getResponseBody();
                        responseStream.write(response.getBytes());
                        responseStream.close();
                    } catch (Exception e) {
                        String errorMessage = e.getMessage();
                        t.sendResponseHeaders(500, errorMessage.length());
                        OutputStream response = t.getResponseBody();
                        response.write(errorMessage.getBytes());
                        response.close();
                    }
                }
            });
            httpServer.setExecutor(null);
            System.out.println("Server starts listening on port: "+Integer.toString(serverPort));
            httpServer.start();
        } catch (Exception e) {
            System.out.println("Error running server: "+e.getMessage());
        }
    }
}
