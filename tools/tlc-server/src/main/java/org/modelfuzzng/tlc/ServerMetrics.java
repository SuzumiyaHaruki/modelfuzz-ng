package org.modelfuzzng.tlc;

import java.util.LinkedHashMap;
import java.util.Map;
import java.util.TreeMap;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.LongAdder;

/** 严格 TLC 服务的累计运行统计；所有时长均使用单调时钟记录。 */
final class ServerMetrics {
    private final LongAdder requests = new LongAdder();
    private final LongAdder succeeded = new LongAdder();
    private final LongAdder modelEvents = new LongAdder();
    private final LongAdder mappingNanos = new LongAdder();
    private final LongAdder successorNanos = new LongAdder();
    private final LongAdder validationNanos = new LongAdder();
    private final LongAdder serializationNanos = new LongAdder();
    private final Map<String, LongAdder> errors = new ConcurrentHashMap<>();

    void request() { requests.increment(); }
    void success() { succeeded.increment(); }
    void events(int count) { modelEvents.add(count); }
    void mapping(long nanos) { mappingNanos.add(nanos); }
    void successor(long nanos) { successorNanos.add(nanos); }
    void validation(long nanos) { validationNanos.add(nanos); }
    void serialization(long nanos) { serializationNanos.add(nanos); }
    void error(String code) { errors.computeIfAbsent(code, ignored -> new LongAdder()).increment(); }

    Map<String, Object> snapshot(RaftEventMapper mapper) {
        Map<String, Long> errorCounts = new TreeMap<>();
        errors.forEach((code, count) -> errorCounts.put(code, count.sum()));
        Map<String, Long> timing = new LinkedHashMap<>();
        timing.put("mapping_nanos", mappingNanos.sum());
        timing.put("action_lookup_nanos", mapper.lookupNanos());
        timing.put("successor_nanos", successorNanos.sum());
        timing.put("validation_nanos", validationNanos.sum());
        timing.put("serialization_nanos", serializationNanos.sum());
        Map<String, Object> result = new LinkedHashMap<>();
        result.put("requests", requests.sum());
        result.put("succeeded", succeeded.sum());
        result.put("failed", requests.sum() - succeeded.sum());
        result.put("model_events", modelEvents.sum());
        result.put("action_lookups", mapper.lookupCount());
        result.put("action_cache_hits", mapper.cacheHitCount());
        result.put("action_cache_misses", mapper.cacheMissCount());
        result.put("actions_created", mapper.actionsCreatedCount());
        result.put("action_cache_evictions", mapper.cacheEvictionCount());
        result.put("cached_actions", mapper.cachedActionCount());
        result.put("errors_by_code", errorCounts);
        result.put("timing", timing);
        return result;
    }
}
