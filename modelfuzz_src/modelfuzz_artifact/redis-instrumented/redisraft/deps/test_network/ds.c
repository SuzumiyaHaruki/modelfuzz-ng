#include "network.h"
#include <stddef.h>
#include <string.h>
#include <stdbool.h>
#include <pthread.h>

redis_test_map* redis_test_create_map(void) {
    redis_test_map* new_m = malloc(sizeof(redis_test_map));
    new_m->elems = redis_test_create_deque();

    return new_m;
}

void redis_test_map_add(redis_test_map* m, const char* key, void* value) {
    redis_test_map_remove(m, key);

    redis_test_map_elem* new = malloc(sizeof(redis_test_map_elem));
    new->key = key;
    new->value = value;
    redis_test_deque_push_back(m->elems, new);
}

void* redis_test_map_remove(redis_test_map* m, const char* key) {
    int index = redis_test_map_exists_index(m,key);
    if (index == -1) {
        // Key does not exist
        return NULL;
    }

    redis_test_map_elem* e = (redis_test_map_elem*) redis_test_deque_remove(m->elems, index);
    void* ret = e->value;
    free(e);
    return ret;
}

int redis_test_map_exists_index(redis_test_map* m, const char* key) {
    redis_test_deque_elem* e = m->elems->head;
    for(int i=0; i< m->elems->size;i++) {
        redis_test_map_elem* m_e = (redis_test_map_elem*) e->elem;
        if(strcmp(m_e->key, key) == 0) {
            return i;
        }
        e = e->next;
    }
    return -1;
}

bool redis_test_map_exists(redis_test_map* m, const char* key) {
    return redis_test_map_exists_index(m, key) != -1;
}

void* redis_test_map_get(redis_test_map* m, const char* key) {
    int index = redis_test_map_exists_index(m, key);
    if (index == -1) {
        return NULL;
    }
    redis_test_map_elem* elem = (redis_test_map_elem *) redis_test_deque_get(m->elems, index);
    return elem->value;
}

int redis_test_map_size(redis_test_map* m) {
    if(m == NULL) {
        return 0;
    }
    return redis_test_deque_size(m->elems);
}

void redis_test_free_map(redis_test_map* m) {
    free(m);
}

redis_test_deque_elem* redis_test_map_iterator(redis_test_map* m) {
    if(m == NULL) {
        return NULL;
    }
    return m->elems->head;
}


redis_test_deque* redis_test_create_deque(void) {
    redis_test_deque* new_deque = malloc(sizeof(redis_test_deque));
    new_deque->head = NULL;
    new_deque->size = 0;

    pthread_mutex_t mutex;
    pthread_mutex_init(&mutex, NULL);
    new_deque->mutex = mutex;
    return new_deque;
}

void* redis_test_deque_remove(redis_test_deque* d, int pos) {
    if (pos >= d->size || pos < 0) {
        return NULL;
    }

    redis_test_deque_elem* elem = d->head;
    for(int i = 0; i != pos; i++) {
        elem = elem->next;
    }

    redis_test_deque_elem* prev = elem->prev;
    redis_test_deque_elem* next = elem->next;
    if (prev == NULL) {
        d->head = next;
    } else {
        prev->next = next;
    }
    if (next != NULL) {
        next->prev = prev;
    }
    
    void* e = elem->elem;
    free(elem);
    d->size--;
    return e;
}

void redis_test_deque_insert(redis_test_deque* d, void* e, int pos) {
    if (pos < 0 || pos > d->size) {
        // Invalid position to insert in
        // will not do anything
        return;
    }
    redis_test_deque_elem* new = malloc(sizeof(redis_test_deque_elem));
    new->elem = e;
    new->prev = NULL;
    new->next = NULL;

    if (pos == 0) {
        new->next = d->head;
        if (d->head != NULL) {
            d->head->prev = new;
        }
        d->head = new;
    } else if (pos == d->size) {
        redis_test_deque_elem* last = d->head;
        while (last->next != NULL)
            last = last->next;

        last->next = new;
        new->prev = last;
    } else {
        redis_test_deque_elem* prev = d->head;
        for(int i = 0; i < pos-1; i++) {
            prev = prev->next;
        }
        new->next = prev->next;
        prev->next = new;
        new->prev = prev;
        if (new->next != NULL) {
            new->next->prev = new;
        }
    }

    d->size++;
    return;
}

void redis_test_deque_push_front(redis_test_deque* d, void* elem) {
    redis_test_deque_insert(d, elem, 0);
}

void redis_test_deque_push_back(redis_test_deque* d, void* elem) {
    redis_test_deque_insert(d, elem, d->size);
}

void* redis_test_deque_pop_front(redis_test_deque* d) {
    return redis_test_deque_remove(d, 0);
}

void* redis_test_deque_pop_back(redis_test_deque* d) {
    return redis_test_deque_remove(d, d->size-1);
}

void* redis_test_deque_get(redis_test_deque* d, int pos) {
    if (pos >= d->size || pos < 0) {
        return NULL;
    }
    redis_test_deque_elem* elem = d->head;
    for(int i = 0; i < pos; i++) {
        elem = elem->next;
    }
    return elem->elem;
}

int redis_test_deque_size(redis_test_deque* d) {
    if (d == NULL) {
        return 0;
    }
    return d->size;
}

void redis_test_free_deque(redis_test_deque* d) {
    pthread_mutex_destroy(&d->mutex);
    free(d);
}

int redis_test_cdeque_insert(redis_test_deque* d, void* elem, int pos) {
    int lock_out = pthread_mutex_lock(&d->mutex);
    if (lock_out != 0) {
        printf("Error locking the thread: %d", lock_out);
        return -1;
    }
    redis_test_deque_insert(d, elem, pos);
    return pthread_mutex_unlock(&d->mutex);
}

void* redis_test_cdeque_remove(redis_test_deque* d, int pos) {
    int lock_out = pthread_mutex_lock(&d->mutex);
    if (lock_out != 0) {
        printf("Error locking the thread: %d", lock_out);
        return -1;
    }
    void* ret = redis_test_deque_remove(d, pos);
    pthread_mutex_unlock(&d->mutex);
    return ret;
}


int redis_test_cdeque_push_front(redis_test_deque* d, void* elem) {
    return redis_test_cdeque_insert(d, elem, 0);
}

int redis_test_cdeque_push_back(redis_test_deque* d, void* elem) {
    return redis_test_cdeque_insert(d, elem, d->size);
}

void* redis_test_cdeque_pop_front(redis_test_deque* d) {
    return redis_test_cdeque_remove(d, 0);
}

void* redis_test_cdeque_pop_back(redis_test_deque* d) {
    return redis_test_cdeque_remove(d, d->size-1);
}

void* redis_test_cdeque_get(redis_test_deque* d, int pos) {
    pthread_mutex_lock(&d->mutex);
    void* ret = redis_test_deque_get(d, pos);
    pthread_mutex_unlock(&d->mutex);
    return ret;
}

int redis_test_cdeque_size(redis_test_deque* d) {
    pthread_mutex_lock(&d->mutex);
    int size = d->size;
    pthread_mutex_unlock(&d->mutex);
    return size;
}

redis_test_deque_elem* redis_test_deque_iterator(redis_test_deque* d) {
    if (d == NULL) {
        return NULL;
    }
    return d->head;
}

redis_test_string* redis_test_create_string(char* a) {
    redis_test_string* s = malloc(sizeof(redis_test_string));
    s->len = 0;
    s->ptr = malloc(s->len+1);
    s->ptr[s->len] = '\0';
    if (a != NULL && strlen(a) != 0) {
        s = redis_test_string_append(s, a);
    }
    return s;
}

redis_test_string* redis_test_string_append(redis_test_string* s, char* str) {
    if (s == NULL) {
        s = redis_test_create_string(NULL);
    }
    if (str == NULL || strlen(str) == 0) {
        return s;
    }
    size_t new_len = s->len + strlen(str);
    s->ptr = realloc(s->ptr, new_len+1);
    memcpy(s->ptr+s->len, str, strlen(str));
    s->ptr[new_len] = '\0';
    s->len = new_len;
    return s;
}

redis_test_string* redis_test_string_appendn(redis_test_string* s, char* str, size_t n) {
    char* s_cpy = malloc(n);
    memcpy(s_cpy, str, n);
    s = redis_test_string_append(s, s_cpy);
    free(s_cpy);
    return s;
}

size_t redis_test_string_len(redis_test_string* s) {
    return s->len;
}

char* redis_test_string_str(redis_test_string* s) {
    char* resp = malloc(s->len+1);
    memcpy(resp, s->ptr, s->len);
    resp[s->len] = '\0';
    return resp;
}

void redis_test_free_string(redis_test_string* s) {
    free(s->ptr);
    free(s);
}

char* redis_test_strndup(const char* str, size_t size) {
    char *buffer;
    int n;

    buffer = (char *) malloc(size+1);
    if (buffer) {
        for (n = 0; ((n < size) && (str[n] != 0)) ; n++) buffer[n] = str[n];
        buffer[n] = 0;
    }

    return buffer;
}