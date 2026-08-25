#include "network.h"
#include <stddef.h>
#include <string.h>
#include <stdint.h>
#include <stdio.h>
#include <json-c/json.h>
#include <time.h>

redis_test_message* redis_test_create_message(char* to, char* data, char* type) {
    redis_test_message* new_message = malloc(sizeof(redis_test_message));
    new_message->to = to;
    new_message->data = data;
    new_message->type = type;
    return new_message;
}

char* redis_test_serialize_message(redis_test_message* message) {
    json_object* obj = json_object_new_object();

    json_object_object_add(obj, "from", json_object_new_string(message->from));
    json_object_object_add(obj, "to", json_object_new_string(message->to));
    json_object_object_add(obj, "data", json_object_new_string(message->data));
    json_object_object_add(obj, "type", json_object_new_string(message->type));
    json_object_object_add(obj, "id", json_object_new_string(message->id));

    char* result = strdup(json_object_to_json_string(obj));

    json_object_put(obj);

    return result;
}

redis_test_message* redis_test_deserialize_message(char* message_s) {
    json_object* obj = json_tokener_parse(message_s);
    if (obj == NULL) {
        return NULL;
    }
    char *from, *to, *data, *type, *id;
    json_object* j_from = json_object_object_get(obj, "from");
    json_object* j_to = json_object_object_get(obj, "to");
    json_object* j_data = json_object_object_get(obj, "data");
    json_object* j_type = json_object_object_get(obj, "type");
    json_object* j_id = json_object_object_get(obj, "id");

    from = strdup(json_object_get_string(j_from));
    to = strdup(json_object_get_string(j_to));
    data = strdup(json_object_get_string(j_data));
    type = strdup(json_object_get_string(j_type));
    id = strdup(json_object_get_string(j_id));

    json_object_put(obj);

    redis_test_message* message = redis_test_create_message(to, data, type);
    message->from = from;
    message->id = id;

    return message;
}

void redis_test_free_message(redis_test_message* message) {
    free(message);
}

redis_test_event* redis_test_create_event(char* type, redis_test_map* params) {
    redis_test_event* new_event = malloc(sizeof(redis_test_event));
    new_event->type = type;
    new_event->params = params;
    new_event->timestamp = (long) time(NULL);
    return new_event;
}

char* redis_test_serialize_event(redis_test_event* event) {
    json_object* obj = json_object_new_object();

    json_object_object_add(obj, "replica", json_object_new_string(event->replica));
    json_object_object_add(obj, "type", json_object_new_string(event->type));
    json_object_object_add(obj, "timestamp", json_object_new_int64((int64_t) event->timestamp));

    struct json_object* params_obj = json_object_new_object();
    if(redis_test_map_size(event->params) != 0) {
        redis_test_deque_elem* e = redis_test_map_iterator(event->params);
        for(;e!= NULL; e = e->next) {
            redis_test_map_elem* map_e = (redis_test_map_elem*) e->elem;
            json_object_object_add(params_obj, map_e->key, json_object_new_string((char*) map_e->value));
        }
    }
    json_object_object_add(obj, "params", params_obj);

    char* result = strdup(json_object_to_json_string(obj));

    json_object_put(obj);

    return result;
}

void redis_test_free_event(redis_test_event* event) {
    free(event);
}