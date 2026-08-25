#include "network.h"
#include "test.h"

#include <assert.h>

redis_test_client* create_mock_client() {
    redis_test_client* new_client = malloc(sizeof(redis_test_client));
    

    return new_client;
}