package tlc2.controlled.protocol;

import java.util.*;
import tlc2.tool.TLCState;
import tlc2.tool.ConcurrentTLCTrace.Record;
import tlc2.value.IValue;
import tlc2.value.impl.FcnRcdValue;
import tlc2.value.impl.IntValue;
import tlc2.value.impl.RecordValue;
import tlc2.value.impl.SetEnumValue;
import tlc2.value.impl.StringValue;
import tlc2.value.impl.Value;
import util.UniqueString;


public class MBStateAbstractor extends DefaultStateAbstractor implements StateAbstractor {
    private boolean isAbstract;



    public MBStateAbstractor(Map<String, String> params) {
        super();
        this.isAbstract = params.containsKey("abstract");
        System.out.println("New MBStateAbstractor instantiated.");
        System.out.println("Abstract: " + this.isAbstract);
    }
    
    List<UniqueString> diff(TLCState one, TLCState two) {
        List<UniqueString> result = new ArrayList<>();
        Map<UniqueString, IValue> oneValues = one.getVals();
        Map<UniqueString, IValue> twoValues = two.getVals();
        for(Map.Entry<UniqueString,IValue>  val: oneValues.entrySet()) {
            if (!twoValues.containsKey(val.getKey())) {
                result.add(val.getKey());
            } else {
                IValue valOne = val.getValue();
                IValue valTwo = twoValues.get(val.getKey());
                if (valOne.compareTo(valTwo) != 0) {
                    result.add(val.getKey());
                }
            }
        }

        return result;
    }

    boolean isDifferent(TLCState cur, TLCState prev) {
        List<UniqueString> diffValues = this.diff(cur, prev);
        return diffValues.size() > 0;
    }

    private TLCState rewrite(TLCState s) {
        if (!this.isAbstract)
            return s;
        
        SetEnumValue currentWorkers = (SetEnumValue) s.getVals().get(UniqueString.of("registeredWorkers"));
        SetEnumValue currentMessages = (SetEnumValue) s.getVals().get(UniqueString.of("msgs"));
        // System.out.println("Rewriting...");
        // System.out.println(currentMessages.toString());
        // System.out.println(currentMessages.getTypeString());
        Value[] newCurrentWorkersValues = new IntValue[currentWorkers.elems.size()];
        for (int i = 0; i < currentWorkers.elems.size(); i++) 
            newCurrentWorkersValues[i] = IntValue.gen(1);
        
        // System.out.println("Current messages:");
        Value[] elems = currentMessages.elems.toArray();
        for (int i = 0; i < elems.length; i++) {
            // RecordValue elem = (RecordValue) elems[i];
            // UniqueString[] names = elem.names;
            for (int j = 0; j < (((RecordValue) elems[i]).names.length); j++) {
                if (((RecordValue) elems[i]).names[j].toString().equals("worker")) {
                    ((RecordValue) elems[i]).values[j] = IntValue.gen(1);
                }
            }
        }
                
        s.bind(UniqueString.of("registeredWorkers"), new SetEnumValue(newCurrentWorkersValues, true));
        s.bind(UniqueString.of("msgs"), new SetEnumValue(elems, true));
        return s;
    }

    @Override
    public List<TLCState> doAbstraction(List<TLCState> states) {
        List<TLCState> superResult = super.doAbstraction(states);
        List<TLCState> result = new ArrayList<>();
        if (superResult.size() == 0) {
            return states;
        }

        int i = 0, j = 1;
        for (; j < superResult.size(); j++) {
            TLCState cur = rewrite(superResult.get(j));
            TLCState prev = rewrite(superResult.get(i));

            if (isDifferent(cur, prev)) {
                result.add(prev);
                i = j;
            }

        }

        if (i == superResult.size() - 1) {
            result.add(superResult.get(i));
        }

        return result;
    }
}
