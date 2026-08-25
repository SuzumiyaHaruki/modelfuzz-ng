#include "network.h"
#include <string.h>
#include <stdio.h>
#include <json-c/json.h>

redis_test_http_reply* handle_message(char *body, void* fn_data) {
    redis_test_message* message = redis_test_deserialize_message(body);
    redis_test_client* client = (redis_test_client*) fn_data;
    while(redis_test_cdeque_push_back(client->message_queue, message) != 0);

    // redis_test_map* params = redis_test_create_map();
    // redis_test_map_add(params, "message_id", strdup(message->id));

    // redis_test_event* e = redis_test_create_event("MessageReceive", params);
    // redis_test_send_event(client, e);
    // redis_test_free_event(e);
    // redis_test_free_map(params);

    redis_test_http_reply* reply = redis_test_http_create_reply();
    reply->body = "OK";
    return reply;
}

int redis_test_send_replica_info(redis_test_client* client, int ready) {
    json_object* obj = json_object_new_object();

    json_object_object_add(obj, "id", json_object_new_string(client->config.id));
    json_object_object_add(obj, "ready", json_object_new_boolean(ready));
    json_object_object_add(obj, "addr", json_object_new_string(client->config.listen_addr));
    json_object* info_obj = json_object_new_object();
    if(redis_test_map_size(client->config.info)!=0) {
        for(redis_test_deque_elem* e= redis_test_map_iterator(client->config.info); e != NULL; e = e->next) {
            redis_test_map_elem* map_e = (redis_test_map_elem*) e->elem;
            json_object_object_add(info_obj, strdup(map_e->key), json_object_new_string(strdup((char*) map_e->value)));
        }
    }
    json_object_object_add(obj, "info", info_obj);
    
    char* replica_data = strdup(json_object_to_json_string(obj));
    json_object_put(obj);
    

    redis_test_string* addr = redis_test_create_string(NULL);
    redis_test_string_append(addr, "http://");
    redis_test_string_append(addr, client->config.redis_test_addr);
    redis_test_string_append(addr, "/replica");

    redis_test_map* headers = redis_test_create_map();
    redis_test_map_add(headers, "Content-Type", "application/json");

    redis_test_http_response* response = redis_test_http_post(redis_test_string_str(addr), replica_data, headers);

    redis_test_free_string(addr);
    redis_test_free_map(headers);

    return response->error_code;
}

void* run_server(void* arg) {
    redis_test_http_server* http_server = (redis_test_http_server *) arg;
    return redis_test_http_listen(http_server);
}

redis_test_client* redis_test_create_client(redis_test_client_config config) {
    redis_test_client* new_client = malloc(sizeof(redis_test_client));
    new_client->config = config;
    new_client->message_queue = redis_test_create_deque();
    redis_test_http_server* server = redis_test_http_create_server(config.listen_addr, new_client);
    redis_test_http_add_handler(server, "/message", handle_message);
    new_client->http_server = server;
    new_client->message_counter = redis_test_create_map();
    new_client->server_thread = (pthread_t) 0;

    if(redis_test_send_replica_info(new_client, 0) != 0) {
        return NULL;
    }
    pthread_create(&new_client->server_thread, NULL, run_server, server);

    return new_client;
}

int redis_test_run_client(redis_test_client* c) {
    return redis_test_send_replica_info(c, 1);
}

void redis_test_signal_client(redis_test_client* c, int signal) {
    if (signal > 0) {
        redis_test_http_signal(c->http_server, signal);
        pthread_join(c->server_thread, NULL);
    }
}

char* get_message_id(redis_test_client* c, char* from, char* to) {
    // TODO: move this to thread safe code.
    redis_test_string* key = redis_test_create_string(from);
    redis_test_string_append(key, "_");
    redis_test_string_append(key, to);

    if(!redis_test_map_exists(c->message_counter, redis_test_string_str(key))) {
        int* count_ptr = malloc(sizeof(int));
        *count_ptr = 0;
        redis_test_map_add(c->message_counter, redis_test_string_str(key), count_ptr);
    }

    int* count_ptr = (int*) redis_test_map_get(c->message_counter, redis_test_string_str(key));
    int count = *count_ptr;
    *count_ptr = count + 1;
    redis_test_map_add(c->message_counter, redis_test_string_str(key), count_ptr);

    char count_s[12];
    sprintf(count_s, "%d", count);

    redis_test_string* val = redis_test_string_append(key, "_");
    redis_test_string_append(val, count_s);

    char* id = redis_test_string_str(val);
    redis_test_free_string(val);
    return id;
}

long redis_test_send_message(redis_test_client* c, redis_test_message* message) {
    message->from = strdup(c->config.id);
    message->id = get_message_id(c, message->from, message->to);

    // redis_test_map* params = redis_test_create_map();
    // redis_test_map_add(params, "message_id", strdup(message->id));
    // redis_test_event* e = redis_test_create_event("MessageSend", params);
    // redis_test_send_event(c, e);
    // redis_test_free_event(e);
    // redis_test_free_map(params);

    redis_test_string* addr = redis_test_create_string(NULL);
    redis_test_string_append(addr, "http://");
    redis_test_string_append(addr, c->config.redis_test_addr);
    redis_test_string_append(addr, "/message");

    redis_test_map* headers = redis_test_create_map();
    redis_test_map_add(headers, "Content-Type", "application/json");

    redis_test_http_response* response = redis_test_http_post(redis_test_string_str(addr), redis_test_serialize_message(message), headers);

    redis_test_free_string(addr);
    redis_test_free_map(headers);

    return response->error_code;
}

long redis_test_send_event(redis_test_client* c, redis_test_event* event) {
    event->replica = c->config.id;

    redis_test_string* addr = redis_test_create_string(NULL);
    redis_test_string_append(addr, "http://");
    redis_test_string_append(addr, c->config.redis_test_addr);
    redis_test_string_append(addr, "/event");

    redis_test_map* headers = redis_test_create_map();
    redis_test_map_add(headers, "Content-Type", "application/json");

    char *serialized_event = redis_test_serialize_event(event);
    redis_test_http_response* response = redis_test_http_post(redis_test_string_str(addr), serialized_event, headers);

    redis_test_free_string(addr);
    redis_test_free_map(headers);

    return response->error_code;
}

bool redis_test_have_message(redis_test_client* c) {
    return redis_test_cdeque_size(c->message_queue) > 0;
}

redis_test_message* redis_test_receive_message(redis_test_client* c) {
    void* val = redis_test_cdeque_pop_front(c->message_queue);

    if (val != NULL) {
        return (redis_test_message*) val;
    }
    return NULL;
}

void redis_test_free_client(redis_test_client* c) {
    redis_test_free_deque(c->message_queue);
    redis_test_free_map(c->message_counter);
    redis_test_http_free_server(c->http_server);
    free(c);
}