#include "test.h"

int main(int argc, char *argv[]) {

    // ds_test.c
    test_run(test_string_create);
    test_run(test_deque_create);
    test_run(test_deque_push_back);
    test_run(test_deque_fetch);
    test_run(test_deque_iter);
    test_run(test_map_exists_index);
    test_run(test_map_exists);
    test_run(test_map_get);

    // json_test.c
    test_run(test_message_serialize_deserialize);
    test_run(test_message_serialize_deserialize_nested);
    test_run(test_message_deserialize);
    
    return 0;
}