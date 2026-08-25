#ifndef redis_test_TEST_H_
#define redis_test_TEST_H_

#include <time.h>
#include <stdio.h>

#define test_run(fn)                                                       \
    do {                                                                   \
        struct timespec ts;                                                \
        unsigned long long start, end;                                     \
        printf("[ Running ] %s \n", #fn);                                  \
                                                                           \
        clock_gettime(CLOCK_MONOTONIC, &ts);                               \
        start = ts.tv_sec * (unsigned long long) 1000000000 + ts.tv_nsec;  \
                                                                           \
        fn();                                                              \
                                                                           \
        clock_gettime(CLOCK_MONOTONIC, &ts);                               \
        end = ts.tv_sec * (unsigned long long) 1000000000 + ts.tv_nsec;    \
                                                                           \
        printf("[ Passed  ] %s in %llu nanoseconds \n", #fn, end - start); \
    } while (0);


// ds_test.c
void test_string_create();
void test_map_exists();
void test_map_exists_index();
void test_map_get();
void test_deque_create();
void test_deque_push_back();
void test_deque_fetch();
void test_deque_iter();

// json_test.c
void test_message_serialize_deserialize();
void test_message_serialize_deserialize_nested();
void test_message_deserialize();

#endif