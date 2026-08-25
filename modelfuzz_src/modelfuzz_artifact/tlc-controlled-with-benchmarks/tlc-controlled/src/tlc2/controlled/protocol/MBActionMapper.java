package tlc2.controlled.protocol;

import tlc2.tool.Action;
import tlc2.value.impl.IntValue;
import tlc2.value.impl.Value;

import java.util.List;
import java.util.Map;
import java.util.Optional;

// MicroBenchmark (MicroBenchmark with parametric number of workers and executes) action mapper
public class MBActionMapper extends BaseActionMapper {

    public MBActionMapper(List<Action> enabledActions) {
        super(enabledActions);
    }

    private Optional<Integer> getWorkerId(AbstractAction abstractAction) {
        if(!abstractAction.params.containsKey("worker_id")) {
            return Optional.empty();
        }
        Object workerIDObject = abstractAction.params.get("worker_id");
        if (workerIDObject instanceof String) {
            Integer workerId = Integer.parseInt((String) abstractAction.params.get("worker_id"));
            return Optional.of(workerId);
        }
        try {
            Double workerId = (Double) workerIDObject;
            return Optional.of(workerId.intValue());
        } catch(Exception e) {
            return Optional.empty();
        }
    }

    protected Action mapPAction(String key) {
        if (!this.enabledActionMap.containsKey(key)) {
            return null;
        } else {
            return this.enabledActionMap.get(key).get(0);
        }
    }

    protected Action mapWAction(String key, int worker) {
        if (!this.enabledActionMap.containsKey(key)) {
            return null;
        }
        for (Action a: this.enabledActionMap.get(key)) {
            Map<String, Value> params = a.getParams();
            if (params.containsKey("w")) {
                IntValue w = (IntValue) params.get("w");
                if (w.val == worker) {
                    return a;
                }
            }
        }
        return null;
    }

    protected Action mapWRAction(String key, int worker, int request) {
        if (!this.enabledActionMap.containsKey(key)) {
            return null;
        }
        for (Action a: this.enabledActionMap.get(key)) {
            Map<String, Value> params = a.getParams();
            if (params.containsKey("w") && params.containsKey("r")) {
                IntValue w = (IntValue) params.get("w");
                IntValue r = (IntValue) params.get("r");
                if (w.val == worker && r.val == request) {
                    return a;
                }
            }
        }
        return null;
    }

    protected Action mapWorkerRegister(int worker) {
        return this.mapWAction("WorkerRegister", worker);
    }

    protected Action mapMasterRegisterWorker(int worker) {
        return this.mapWAction("MasterRegisterWorker", worker);
    }

    protected Action mapTerminatorRegister() {
        return this.mapPAction("TerminatorRegister");
    }

    protected Action mapMasterRegisterTerminator() {
        return this.mapPAction("MasterRegisterTerminator");
    }

    protected Action mapMasterRcvRequest(int worker, int request) {
        return this.mapWRAction("MasterRcvRequest", worker, request);
    }

    protected Action mapWorkerRcvExecute(int worker, int request) {
        return this.mapWRAction("WorkerRcvExecute", worker, request);
    }

    protected Action mapTerminatorRcvTerminate(int worker) {
        return this.mapWAction("TerminatorRcvTerminate", worker);
    }

    protected Action mapWorkerRcvFlush(int worker) {
        return this.mapWAction("WorkerRcvFlush", worker);
    }

    public Action mapAction(AbstractAction abstractAction) {
        try{
            String message = abstractAction.name;
            switch (message) {
                case "SendEvent":
                    String event = (String) abstractAction.params.get("event");
                    switch (event) {
                        case "MicroBenchmark.RegisterWorkerEvent":
                            Optional<Integer> workerId = getWorkerId(abstractAction);
                            if (workerId.isEmpty()) {
                                return null;
                            }
                            int worker = workerId.get();
                            return this.mapWorkerRegister(worker);
                        case "MicroBenchmark.RegisterTerminatorEvent":
                            return this.mapTerminatorRegister();
                        }
                    break;
                case "ReceiveEvent":
                    event = (String) abstractAction.params.get("event");
                    Optional<Integer> workerId = getWorkerId(abstractAction);
                    if (workerId.isEmpty()) {
                        return null;
                    }
                    int w_val = workerId.get();
                    switch (event) {
                        case "MicroBenchmark.ExecuteEvent":
                            int r_val = (int)((double) abstractAction.params.get("request_id"));
                            return this.mapWorkerRcvExecute(w_val, r_val);
                        case "MicroBenchmark.TerminateEvent":
                            return this.mapTerminatorRcvTerminate(w_val);
                        case "MicroBenchmark.FlushEvent":
                            return this.mapWorkerRcvFlush(w_val);
                        case "MicroBenchmark.RequestEvent":
                            r_val = (int)((double) abstractAction.params.get("request_id"));
                            return this.mapMasterRcvRequest(w_val, r_val);
                        case "MicroBenchmark.RegisterWorkerEvent":
                            return mapMasterRegisterWorker(w_val);
                    }
                    break;
                case "InvokedAction":
                    event = (String) abstractAction.params.get("action");
                    switch (event) {
                        case "HandleRegisterTerminator":
                            return mapMasterRegisterTerminator();
                    }
                    break;
            }
            return null;
        } catch (Exception e) {
            System.out.println("[EAActionMapper] Invalid action");
            System.out.println("Error: "+e.getMessage());
        }

        return null;
    }

}
