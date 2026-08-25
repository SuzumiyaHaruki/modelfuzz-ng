#include "network.h"
#include "test.h"

#include <assert.h>
#include <string.h>
#include <stdbool.h>

void test_string_create() {
    redis_test_string* s = redis_test_create_string("hello");
    char *out = redis_test_string_str(s);

    int check = strcmp("hello", out);
    assert(check == 0);

    redis_test_free_string(s);
}

void test_map_exists_index() {
    redis_test_map* m = redis_test_create_map();
    redis_test_map_add(m, "test", "test");
    int index = redis_test_map_exists_index(m, "test");

    assert(index == 0);

    redis_test_free_map(m);
}

void test_map_exists() {
    redis_test_map* m = redis_test_create_map();
    bool exists = redis_test_map_exists(m, "test");

    assert(exists == 0);

    redis_test_free_map(m);
}

void test_map_get() {
    redis_test_map* m = redis_test_create_map();
    redis_test_map_add(m, "test", "test");

    char *val = redis_test_map_get(m, "test");

    assert(strcmp(val, "test") == 0);

    redis_test_free_map(m);
}

void test_deque_create() {
    redis_test_deque* d = redis_test_create_deque();

    assert(redis_test_deque_size(d) == 0);

    redis_test_free_deque(d);
}

void test_deque_push_back() {
    redis_test_deque* d = redis_test_create_deque();
    redis_test_deque_push_back(d, 1);
    redis_test_deque_push_back(d, 2);

    assert(redis_test_deque_size(d) == 2);

    redis_test_free_deque(d);
}

void test_deque_fetch() {
    redis_test_deque* d = redis_test_create_deque();
    redis_test_deque_push_back(d, 1);

    assert(redis_test_deque_size(d) == 1);

    redis_test_deque_elem* e = d->head;
    int val = (int) e->elem;

    assert(val == 1);

    redis_test_free_deque(d);
}

void test_deque_iter() {
    int elems[3] = {1,2,3};

    redis_test_deque* d= redis_test_create_deque();
    redis_test_deque_push_back(d, 1);
    redis_test_deque_push_back(d, 2);
    redis_test_deque_push_back(d, 3);

    redis_test_deque_elem* e = d->head;
    for(int i=0; i < d->size;i++) {
        assert(e != NULL);
        int m_e = (int) e->elem;
        assert(m_e == elems[i]);
        e = e->next;
    }

    redis_test_free_deque(d);
}