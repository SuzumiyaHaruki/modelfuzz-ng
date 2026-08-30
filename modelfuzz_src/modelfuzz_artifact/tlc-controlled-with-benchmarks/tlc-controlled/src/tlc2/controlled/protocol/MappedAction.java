package tlc2.controlled.protocol;

/**
 * Preserves the source JSON event index even when it does not map to a TLA+
 * action.  The legacy mapper API filters such events; TLCServer uses this form
 * to return one provenance record for every input event.
 */
public final class MappedAction {
    private final int inputIndex;
    private final String inputName;
    private final ActionWrapper action;

    private MappedAction(int inputIndex, String inputName, ActionWrapper action) {
        this.inputIndex = inputIndex;
        this.inputName = inputName;
        this.action = action;
    }

    public static MappedAction mapped(int inputIndex, String inputName, ActionWrapper action) {
        return new MappedAction(inputIndex, inputName, action);
    }

    public static MappedAction ignored(int inputIndex, String inputName) {
        return new MappedAction(inputIndex, inputName, null);
    }

    public int getInputIndex() {
        return inputIndex;
    }

    public String getInputName() {
        return inputName;
    }

    public ActionWrapper getAction() {
        return action;
    }

    public boolean isIgnored() {
        return action == null;
    }
}
