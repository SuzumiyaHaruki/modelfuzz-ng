#include "network.h"
#include "test.h"

#include <assert.h>
#include <string.h>
#include <stdbool.h>

void test_message_serialize_deserialize() {

    redis_test_message* m = redis_test_create_message("1", "data", "test_message");
    m->from = "2";
    m->id = "2_1_1";
    char* serialized_message = redis_test_serialize_message(m);
    redis_test_free_message(m);

    m = redis_test_deserialize_message(serialized_message);

    assert(strcmp(m->id, "2_1_1") == 0);
    redis_test_free_message(m);
}

void test_message_serialize_deserialize_nested() {
    redis_test_map* params = redis_test_create_map();
    redis_test_map_add(params, "one", "one");
    redis_test_map_add(params, "two", "two");
    redis_test_event* e = redis_test_create_event("test_event", params);
    e->replica = "1";

    redis_test_message* m = redis_test_create_message("1", redis_test_serialize_event(e), "test_message");
    m->from = "2";
    m->id = "2_1_1";

    char* serialized_message = redis_test_serialize_message(m);
    redis_test_free_message(m);

    m = redis_test_deserialize_message(serialized_message);

    assert(strcmp(m->id, "2_1_1") == 0);

    redis_test_free_message(m);
    redis_test_free_event(e);
}

void test_message_deserialize() {
    char *test_msg = "{\"from\": \"1\", \"to\": \"2\", \"type\": \"append_entries_request\", \"data\": \"{ \\\"type\\\": \\\"append_entries_request\\\", \\\"leader_id\\\": 1, \\\"term\\\": 1.0, \\\"prev_log_idx\\\": 0.0, \\\"prev_log_term\\\": 0.0, \\\"leader_commit\\\": 2.0, \\\"msg_id\\\": 1.0, \\\"entries\\\": [ { \\\"term\\\": 1.0, \\\"id\\\": 0, \\\"session\\\": 0.0, \\\"type\\\": 4, \\\"data\\\": \\\"\\\" }, { \\\"term\\\": 1.0, \\\"id\\\": 1096054577, \\\"session\\\": 0.0, \\\"type\\\": 2, \\\"data\\\": \\\"AQAAAIkTbG9jYWxob3N0AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\\\" }, { \\\"term\\\": 1.0, \\\"id\\\": 274551673, \\\"session\\\": 0.0, \\\"type\\\": 1, \\\"data\\\": \\\"AgAAAIoTbG9jYWxob3N0AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\\\" } ] }\", \"id\": \"1_2_0\"}";

    redis_test_message* m = redis_test_deserialize_message(test_msg);

    assert(strcmp(m->id, "1_2_0") == 0);

    redis_test_free_message(m);
}