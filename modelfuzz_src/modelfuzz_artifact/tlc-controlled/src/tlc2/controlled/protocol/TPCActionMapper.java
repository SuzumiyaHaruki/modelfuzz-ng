package tlc2.controlled.protocol;

import tlc2.tool.Action;
import tlc2.value.impl.EnumerableValue;
import tlc2.value.impl.FcnRcdValue;
import tlc2.value.impl.IntValue;
import tlc2.value.impl.SetEnumValue;
import tlc2.value.impl.Value;
import tlc2.value.impl.ValueEnumeration;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Map;
import java.util.Optional;

// TwoPhaseCommit (TwoPhaseCommit with parametric number of transaction requests) action mapper
public class TPCActionMapper extends BaseActionMapper {
    
    private boolean isAbstract;

    public TPCActionMapper(List<Action> enabledActions, boolean isAbstract) {
        super(enabledActions);
        this.isAbstract = isAbstract;
        System.out.println("Starting TPCActionMapper...");
    }

    private Optional<Integer> getRequestId(AbstractAction abstractAction) {
        if(!abstractAction.params.containsKey("request_id")) {
            return Optional.empty();
        }
        Object requestIDObject = abstractAction.params.get("request_id");
        if (requestIDObject instanceof String) {
            Integer requestId = Integer.parseInt((String) abstractAction.params.get("request_id"));
            return Optional.of(requestId);
        }
        try {
            Double requestID = (Double) requestIDObject;
            return Optional.of(requestID.intValue());
        } catch(Exception e) {
            return Optional.empty();
        }
    }

    protected Action mapIAction(String key, int request) {
        if (!this.enabledActionMap.containsKey(key)) {
            return null;
        }
        for (Action a: this.enabledActionMap.get(key)) {
            Map<String, Value> params = a.getParams();
            if (params.containsKey("i")) {
                IntValue i = (IntValue) params.get("i");
                if (i.val == request) {
                    return a;
                }
            }
        }
        return null;
    }

    protected Action mapRIAction(String key, int rm, int request) {
        if (!this.enabledActionMap.containsKey(key)) {
            return null;
        }
        for (Action a: this.enabledActionMap.get(key)) {
            Map<String, Value> params = a.getParams();
            if (params.containsKey("i") && params.containsKey("r")) {
                IntValue i = (IntValue) params.get("i");
                IntValue r = (IntValue) params.get("r");
                if (i.val == request && r.val == rm) {
                    return a;
                }
            }
        }
        return null;
    }

    // TODO
    protected Action mapRIVAction(String key, int rm, int request, int[] vars) {
        if (!this.enabledActionMap.containsKey(key)) {
            return null;
        }

        // for(int var : vars) {
        //     System.out.println(var);
        // }

        // for (Action a: this.enabledActionMap.get(key)) {
        //     Map<String, Value> params = a.getParams();
        //     SetEnumValue V = (SetEnumValue) params.get("v");
        //     System.out.println(V.elems.toString());
        // }

        for (Action a: this.enabledActionMap.get(key)) {
            Map<String, Value> params = a.getParams();
            if (params.containsKey("i") && params.containsKey("r") && params.containsKey("v")) {
                IntValue i = (IntValue) params.get("i");
                IntValue r = (IntValue) params.get("r");
                SetEnumValue V = (SetEnumValue) params.get("v");
                // IntValue v = (IntValue) params.get("v");
                if (i.val == request && r.val == rm) { // && v.val == vars) {
                    // return a;
                    if (V.elems.size() != vars.length)
                        continue;
                    else {
                        boolean b = true;
                        for (int j = 0; j < vars.length; j++) {
                            IntValue v = (IntValue) V.elems.elementAt(j);
                            if (v.val != vars[j]) {
                                b = false;
                                break;
                            }
                        }
                        if (b)
                            return a;
                    }
                }
            }
        }
        return null;
    }

    // protected Action mapVAction(String key, int[] varsArr) {
    //     if (!this.enabledActionMap.containsKey(key)) {
    //         return null;
    //     }
    //     for (Action a: this.enabledActionMap.get(key)) {
    //         Map<String, Value> params = a.getParams();
            
    //         if (params.containsKey("v")) {
    //             SetEnumValue V = (SetEnumValue) params.get("v");
    //             System.out.println(V.elements());
    //             System.out.println(V.elements().nextElement().getClass().getName());
    //             System.out.println(V.elements().toString());
    //             System.out.println(V.elems.toArray().toString());
    //             // Value vals[] = V.elems.toArray();
    //             // IntValue intvals[] = new IntValue[vals.length];
    //             // for (int i = 0; i < vals.length; i++) {
    //             //     intvals[i] = (IntValue) vals[i];
    //             // }
    //             // boolean b = true;
    //             // for (int i = 0; i < intvals.length; i++) {
    //             //     if (intvals[i].val != varsArr[i])
    //             //         b = false;
    //             // }
    //             // if (b)
    //             //     return a;
    //         }
    //     }
    //     return null;
    // }

    protected Action mapClientRequest(int requestID) {
        // if (!this.enabledActionMap.containsKey("NextRequest")) {
        //     return null;
        // }
        // return this.enabledActionMap.get("NextRequest").get(0); 
        return this.mapIAction("NextRequest", requestID);
    }

    protected Action mapTMSendPrepared(int requestID) {
        if (this.isAbstract) {
            return null;
        }
        return this.mapIAction("TMSendPrepareReq", requestID);
    }

    protected Action mapTMSendGlobalCommit(int requestID) {
        if (this.isAbstract) {
            return null;
        }
        return this.mapIAction("TMSendGlobalCommit", requestID);
    }

    protected Action mapTMRcvPrepared(int rm, int requestID) {
        if (this.isAbstract) {
            return null;
        }
        return this.mapRIAction("TMRcvPrepared", rm, requestID);
    }

    protected Action mapTMRcvAborted(int rm, int requestID) {
        if (this.isAbstract) {
            return null;
        }
        return this.mapRIAction("TMRcvAborted", rm, requestID);
    }

    protected Action mapRMSendPrepared(int rm, int requestID, ArrayList<Double> vars) {
        String key = this.isAbstract ? "RMPrepared" : "RMSendPrepared";
        return this.mapRIVAction(key, rm, requestID, toIntArray(vars));
    }

    protected Action mapRMSendAborted(int rm, int requestID, ArrayList<Double> vars) {
        String key = this.isAbstract ? "RMAborted" : "RMSendAborted";
        return this.mapRIVAction(key, rm, requestID, toIntArray(vars));
    }

    protected Action mapRMRcvPrepareReq(int rm, int requestID) {
        if (this.isAbstract) {
            return null;
        }
        return this.mapRIAction("RMRcvPrepareReq", rm, requestID);
    }

    protected Action mapRMRcvGlobalAbort(int rm, int requestID, ArrayList<Double> vars) {
        String key = this.isAbstract ? "RMAborted" : "RMRcvGlobalAbort";
        return this.mapRIVAction(key, rm, requestID, toIntArray(vars));
    }

    protected Action mapRMRcvGlobalCommit(int rm, int requestID, ArrayList<Double> vars) {
        String key = this.isAbstract ? "RMCommitted" : "RMRcvGlobalCommit";
        return this.mapRIVAction(key, rm, requestID, toIntArray(vars));
    }

    private int[] toIntArray(ArrayList<Double> arr) {
        // System.out.println(arr);
        int[] intArr = new int[arr.size()];
        for(int i = 0; i < arr.size(); i++)
            intArr[i] = arr.get(i).intValue();

        Arrays.sort(intArr);
        return intArr;
    }

    public Action mapAction(AbstractAction abstractAction) {
        try{
            String message = abstractAction.name;
            
            switch (message) {
                case "SendEvent":
                    String event = (String) abstractAction.params.get("event");
                    switch (event) {
                        case "NextRequest":
                            Optional<Integer> requestId = getRequestId(abstractAction);
                            if (requestId.isEmpty()) {
                                return null;
                            }
                            int request = requestId.get();
                            return this.mapClientRequest(request);
                        case "TMSendPrepareReq":
                            requestId = getRequestId(abstractAction);
                            if (requestId.isEmpty()) {
                                return null;
                            }
                            request = requestId.get();

                            if (!this.enabledActionMap.containsKey("TMSendPrepareReq")) {
                                return null;
                            }
                            return this.mapTMSendPrepared(request);
                        case "RMSendPrepared":
                            int sender = Integer.parseInt((String) abstractAction.params.get("sender_id"));
                            ArrayList<Double> vars = ((ArrayList<Double>)abstractAction.params.get("vars"));
                            requestId = getRequestId(abstractAction);
                            if (requestId.isEmpty()) {
                                return null;
                            }
                            return this.mapRMSendPrepared(sender, requestId.get(), vars);
                        case "RMSendAborted":
                            sender = Integer.parseInt((String) abstractAction.params.get("sender_id"));
                            requestId = getRequestId(abstractAction);
                            if (requestId.isEmpty()) {
                                return null;
                            }
                            vars = ((ArrayList<Double>)abstractAction.params.get("vars"));
                            return this.mapRMSendAborted(sender, requestId.get(), vars);
                            // Map to "RMSendAborted"
                        case "TMSendGlobalCommit":
                            requestId = getRequestId(abstractAction);
                            if (requestId.isEmpty()) {
                                return null;
                            }
                            return this.mapTMSendGlobalCommit(requestId.get());
                            // Map to "TMSendGlobalCommit"
                        case "TwoPhaseCommit.GlobalAbortEvent":
                            break;
                            // Map to "TMSendGlobalAbort" but the model doesn't have corresponding action for this
                    }
                    break;
                case "ReceiveEvent":
                    event = (String) abstractAction.params.get("event");
                    Optional<Integer> requestId = getRequestId(abstractAction);
                    if (requestId.isEmpty()) {
                        return null;
                    }
                    int i_val = requestId.get();
                    switch (event) {
                        case "RMRcvPrepareReq":
                            int r_val = Integer.parseInt((String) abstractAction.params.get("receiver_id"));
                            return this.mapRMRcvPrepareReq(r_val, i_val);
                            // Map to "RMRcvPrepareReq"
                        case "TMRcvPrepared":
                            r_val = Integer.parseInt((String) abstractAction.params.get("sender_id"));
                            return this.mapTMRcvPrepared(r_val, i_val);
                            // Map to "TMRcvPrepared"
                        case "TMRcvAborted":
                            r_val = Integer.parseInt((String) abstractAction.params.get("sender_id"));
                            return this.mapTMRcvAborted(r_val, i_val);
                            // Map to "TMRcvAborted"
                        case "RMRcvGlobalAbort":
                            r_val = Integer.parseInt((String) abstractAction.params.get("receiver_id"));
                            ArrayList<Double> vars = ((ArrayList<Double>)abstractAction.params.get("vars"));
                            return this.mapRMRcvGlobalAbort(r_val, i_val, vars);
                            // Map to "RMRcvGlobalAbort"
                        case "RMRcvGlobalCommit":
                            r_val = Integer.parseInt((String) abstractAction.params.get("receiver_id"));
                            vars = ((ArrayList<Double>)abstractAction.params.get("vars"));
                            return this.mapRMRcvGlobalCommit(r_val, i_val, vars);
                            // Map to "RMRcvGlobalCommit"
                    }
                    break;
            }
            return null;
        } catch (Exception e) {
            System.out.println("[TPCActionMapper] Invalid action");
            System.out.println("Error: "+e.getMessage());
            e.printStackTrace();
        }

        return null;
    }

}
