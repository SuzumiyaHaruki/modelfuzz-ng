package org.modelfuzzng.tlc;

final class ProtocolException extends Exception {
    private final String code;
    private final int eventIndex;
    private final String eventName;
    private final int httpStatus;

    ProtocolException(String code, int eventIndex, String eventName, int httpStatus, String message) {
        super(message);
        this.code = code;
        this.eventIndex = eventIndex;
        this.eventName = eventName == null ? "" : eventName;
        this.httpStatus = httpStatus;
    }

    String code() {
        return code;
    }

    int eventIndex() {
        return eventIndex;
    }

    String eventName() {
        return eventName;
    }

    int httpStatus() {
        return httpStatus;
    }
}
