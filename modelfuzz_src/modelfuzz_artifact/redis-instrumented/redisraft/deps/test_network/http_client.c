#include "network.h"
#include <curl/curl.h>
#include <stdio.h>
#include <string.h>

redis_test_http_response* redis_test_http_create_response(void) {
    redis_test_http_response* resp = malloc(sizeof(redis_test_http_response));
    return resp;
}

void redis_test_http_free_response(redis_test_http_response* resp) {
    free(resp);
}

typedef struct http_post_fn_data {
    bool done;
    redis_test_http_response* response;
} http_post_fn_data;

redis_test_http_response* redis_test_http_post(char* url, char* body, redis_test_map* headers) {
    redis_test_http_request request;
    request.method = "POST";
    request.body = body;
    request.headers = headers;
    request.url = url;

    return redis_test_http_do(&request);
}

redis_test_http_response* redis_test_http_get(char* url, redis_test_map* headers) {
    redis_test_http_request request;
    request.method = "GET";
    request.url = url;
    request.headers = headers;

    return redis_test_http_do(&request);
}

static size_t request_cb(char* ptr, size_t size, size_t nemb, redis_test_string* resp) {
    // Send the request and update the response
    size_t real_size = size*nemb;
    redis_test_string_appendn(resp, ptr, real_size);
    return real_size;
}

redis_test_http_response* redis_test_http_do(redis_test_http_request* req) {
    if (req == NULL) {
        redis_test_http_response* resp = redis_test_http_create_response();
        resp->error = "Empty request";
        resp->error_code = 1;
        resp->response_body = NULL;
        return resp;
    }

    redis_test_http_response* resp = redis_test_http_create_response();
    curl_global_init(CURL_GLOBAL_ALL);

    CURL* handle = curl_easy_init();
    if (handle) {
        curl_easy_setopt(handle, CURLOPT_URL, req->url);
        redis_test_string* resp_s = redis_test_create_string(NULL);
        
        curl_easy_setopt(handle, CURLOPT_WRITEDATA, resp_s);
        curl_easy_setopt(handle, CURLOPT_WRITEFUNCTION, request_cb);

        struct curl_slist* headers = NULL;
        if(redis_test_map_size(req->headers) != 0) {
            redis_test_deque_elem* elem = redis_test_map_iterator(req->headers);
            for (;elem != NULL; elem = elem->next) {
                redis_test_map_elem* header = (redis_test_map_elem*) elem->elem;
                redis_test_string* header_s = redis_test_create_string((char*) header->key);
                redis_test_string_append(header_s, ": ");
                redis_test_string_append(header_s, (char*) header->value);
                headers = curl_slist_append(headers, header_s->ptr);
            }

            curl_easy_setopt(handle, CURLOPT_HEADER, headers);
        }
        
        if(strcmp(req->method, "POST") == 0) {
            curl_easy_setopt(handle, CURLOPT_POSTFIELDS, req->body);
        }
        CURLcode res = curl_easy_perform(handle);
        if (res == CURLE_OK) {
            long resp_code;
            curl_easy_getinfo(handle, CURLINFO_RESPONSE_CODE, &resp_code);
            if (resp_code >= 400) {
                resp->error_code = resp_code;
                resp->error = redis_test_string_str(resp_s);
            } else {
                resp->error_code = 0;
                resp->error = NULL;
                resp->response_body = redis_test_string_str(resp_s);
            }
        } else {
            resp->error_code = res;
            resp->error = curl_easy_strerror(res);
        }
        redis_test_free_string(resp_s);
        curl_easy_cleanup(handle);
    }
    curl_global_cleanup();
    return resp;
}