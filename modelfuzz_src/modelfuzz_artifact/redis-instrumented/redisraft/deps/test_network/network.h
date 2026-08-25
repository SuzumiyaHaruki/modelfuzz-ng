#ifndef TEST_NETWORK_H_
#define TEST_NETWORK_H_

#include "mongoose.h"
#include <pthread.h>

#include <stdbool.h>
#include <stdlib.h>
#include <pthread.h>

/* ds.c */

// Generic Deque element that stores elements as void*
typedef struct redis_test_deque_elem {
    void* elem;
    struct redis_test_deque_elem* next;
    struct redis_test_deque_elem* prev;
} redis_test_deque_elem;

// Generic doubly linked list
// Includes mutex to enable thread safe operations
typedef struct redis_test_deque {
    redis_test_deque_elem* head;
    int size;
    pthread_mutex_t mutex;
} redis_test_deque;

// Creation and destruction of deque
redis_test_deque* redis_test_create_deque(void);
void redis_test_free_deque(redis_test_deque*);

// Deque operations
void redis_test_deque_push_front(redis_test_deque*, void*);
void redis_test_deque_push_back(redis_test_deque*, void*);
void* redis_test_deque_pop_front(redis_test_deque*);
void* redis_test_deque_pop_back(redis_test_deque*);
void* redis_test_deque_remove(redis_test_deque*, int);
void redis_test_deque_insert(redis_test_deque*, void*, int);
int redis_test_deque_size(redis_test_deque*);
void* redis_test_deque_get(redis_test_deque*, int);

// Thread safe deque operations
int redis_test_cdeque_insert(redis_test_deque*, void*, int);
void* redis_test_cdeque_remove(redis_test_deque*, int);
int redis_test_cdeque_push_front(redis_test_deque*, void*);
int redis_test_cdeque_push_back(redis_test_deque*, void*);
void* redis_test_cdeque_pop_front(redis_test_deque*);
void* redis_test_cdeque_pop_back(redis_test_deque*);
void* redis_test_cdeque_get(redis_test_deque*, int);
int redis_test_cdeque_size(redis_test_deque*);

// Deque iteration
redis_test_deque_elem* redis_test_deque_iterator(redis_test_deque*);

typedef struct redis_test_map_elem {
    const char* key;
    void* value;
} redis_test_map_elem;

typedef struct redis_test_map {
    redis_test_deque* elems;
} redis_test_map;

redis_test_map* redis_test_create_map(void);
void redis_test_map_add(redis_test_map*, const char*, void*);
void* redis_test_map_remove(redis_test_map*, const char*);
int redis_test_map_exists_index(redis_test_map*, const char*);
bool redis_test_map_exists(redis_test_map*, const char*);
void* redis_test_map_get(redis_test_map*, const char*);
int redis_test_map_size(redis_test_map*);
void redis_test_free_map(redis_test_map*);

redis_test_deque_elem* redis_test_map_iterator(redis_test_map*);

// String operations
typedef struct redis_test_string {
  char *ptr;
  size_t len;
} redis_test_string;

redis_test_string* redis_test_create_string(char*);
redis_test_string* redis_test_string_append(redis_test_string*, char*);
redis_test_string* redis_test_string_appendn(redis_test_string*, char*, size_t n);
char* redis_test_string_str(redis_test_string*);
size_t redis_test_string_len(redis_test_string*);
void redis_test_free_string(redis_test_string*);

char* redis_test_strndup(const char*, size_t);

/* http_server.c */

typedef struct mg_mgr mg_mgr;

typedef struct redis_test_http_reply {
    int status_code;
    char* headers;
    char* body;
} redis_test_http_reply;

redis_test_http_reply* redis_test_http_create_reply(void);
void redis_test_http_free_reply(redis_test_http_reply*);

// TODO create a custom http_message type and send that to handler
typedef redis_test_http_reply* (*redis_test_http_handler)(char* body, void* fn_data);

typedef struct redis_test_http_server {
    const char* listen_addr;
    redis_test_map* handlers;
    mg_mgr* mg_mgr;
    void* fn_data;
    int signal;
} redis_test_http_server;

redis_test_http_server* redis_test_http_create_server(const char*, void* fn_data);
void redis_test_http_add_handler(redis_test_http_server*, const char*, redis_test_http_handler);
void* redis_test_http_listen(redis_test_http_server*);
void redis_test_http_signal(redis_test_http_server*, int);
void redis_test_http_free_server(redis_test_http_server*);

/* http_client.c */

typedef struct redis_test_http_request {
    char* body;
    char* method;
    redis_test_map* headers;
    char* url;
} redis_test_http_request;

typedef struct redis_test_http_response {
    long error_code;
    char* error;
    char* response_body;
} redis_test_http_response;

redis_test_http_response* redis_test_http_create_response(void);
void redis_test_http_free_response(redis_test_http_response*);

redis_test_http_response* redis_test_http_post(char* url, char* body, redis_test_map* headers);
redis_test_http_response* redis_test_http_get(char* url, redis_test_map* headers);

redis_test_http_response* redis_test_http_do(redis_test_http_request*);

/* types.c */

typedef struct redis_test_message {
    char* from;
    char* to;
    char* data;
    char* type;
    char* id;
} redis_test_message;

typedef struct redis_test_event {
    char* type;
    char* replica;
    redis_test_map* params;
    long timestamp;
} redis_test_event;

redis_test_message* redis_test_create_message(char*, char*, char*);
char* redis_test_serialize_message(redis_test_message*);
redis_test_message* redis_test_deserialize_message(char*);
void redis_test_free_message(redis_test_message*);

redis_test_event* redis_test_create_event(char*, redis_test_map*);
char* redis_test_serialize_event(redis_test_event*);
void redis_test_free_event(redis_test_event*);

/* client.c */

typedef struct redis_test_client_config {
    const char* id;
    const char* redis_test_addr;
    const char* listen_addr;
    redis_test_map* info;
    void* user_data;
} redis_test_client_config;

typedef struct redis_test_client {
    redis_test_client_config config;
    redis_test_deque* message_queue;
    redis_test_http_server* http_server;
    redis_test_map* message_counter;
    pthread_t server_thread;
} redis_test_client;

redis_test_client* redis_test_create_client(redis_test_client_config);
int redis_test_run_client(redis_test_client*);
void redis_test_signal_client(redis_test_client*, int);
long redis_test_send_message(redis_test_client*, redis_test_message*);
long redis_test_send_event(redis_test_client*, redis_test_event*);
bool redis_test_have_message(redis_test_client*);
redis_test_message* redis_test_receive_message(redis_test_client*);
void redis_test_free_client(redis_test_client*);

#endif