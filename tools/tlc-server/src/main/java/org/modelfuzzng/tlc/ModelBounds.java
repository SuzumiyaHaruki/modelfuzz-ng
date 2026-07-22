package org.modelfuzzng.tlc;

import java.util.Collections;
import java.util.HashSet;
import java.util.Set;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** 从 controlled TLC 配置中提取映射所需的有限参数边界。 */
final class ModelBounds {
    private final Set<Long> servers;
    private final Long largestTerm;
    private final Long maxLogIndex;
    private final Long maxValue;
    private final Long nil;

    private ModelBounds(Set<Long> servers, Long largestTerm, Long maxLogIndex, Long maxValue, Long nil) {
        this.servers = servers;
        this.largestTerm = largestTerm;
        this.maxLogIndex = maxLogIndex;
        this.maxValue = maxValue;
        this.nil = nil;
    }

    static ModelBounds parse(String config) {
        return new ModelBounds(
            integerSet(config, "Server"),
            integer(config, "LargestTerm"),
            integer(config, "MaxLogIndex"),
            integer(config, "MaxValue"),
            integer(config, "Nil")
        );
    }

    boolean contains(String parameter, long value) {
        return switch (parameter) {
            case "i", "j" -> servers.isEmpty() || servers.contains(value);
            case "term", "lTerm", "pLogTerm", "entryTerm" ->
                largestTerm == null || value >= 0 && value <= largestTerm;
            case "lIndex", "pLogIndex", "cIndex", "mIndex" ->
                maxLogIndex == null || value >= 0 && value <= maxLogIndex;
            case "v", "entryValue" -> maxValue == null
                || value >= 1 && value <= maxValue
                || nil != null && value == nil;
            default -> true;
        };
    }

    List<Long> serverIDs() {
        return servers.stream().sorted().toList();
    }

    Long largestTerm() {
        return largestTerm;
    }

    Long maxLogIndex() {
        return maxLogIndex;
    }

    Long maxValue() {
        return maxValue;
    }

    Long nilValue() {
        return nil;
    }

    private static Long integer(String config, String name) {
        Matcher matcher = Pattern.compile(
            "(?m)^\\s*" + Pattern.quote(name) + "\\s*=\\s*(-?\\d+)\\s*$"
        ).matcher(config);
        return matcher.find() ? Long.parseLong(matcher.group(1)) : null;
    }

    private static Set<Long> integerSet(String config, String name) {
        Matcher matcher = Pattern.compile(
            "(?m)^\\s*" + Pattern.quote(name) + "\\s*=\\s*\\{([^}]*)}\\s*$"
        ).matcher(config);
        if (!matcher.find()) {
            return Set.of();
        }
        Set<Long> values = new HashSet<>();
        for (String item : matcher.group(1).split(",")) {
            String value = item.trim();
            if (!value.isEmpty()) {
                values.add(Long.parseLong(value));
            }
        }
        return Collections.unmodifiableSet(values);
    }
}
