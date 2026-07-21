package org.modelfuzzng.tlc;

import tlc2.tool.Action;

record MappedEvent(String externalName, Action action, boolean reset) {
    static MappedEvent resetEvent() {
        return new MappedEvent("reset", null, true);
    }

    static MappedEvent action(String externalName, Action action) {
        return new MappedEvent(externalName, action, false);
    }
}
