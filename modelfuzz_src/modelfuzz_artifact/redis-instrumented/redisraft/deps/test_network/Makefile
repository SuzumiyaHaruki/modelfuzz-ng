SHELL := /bin/sh
CC := gcc
WFLAGS := -Wno-void-pointer-to-int-cast -Wno-int-to-void-pointer-cast -Wno-incompatible-pointer-types-discards-qualifiers -Wno-int-conversion -fPIC
CFLAGS := -I/usr/local/include -I/home/snagendra/Tools/json-c/include $(WFLAGS)
LDFLAGS := -shared -L/usr/local/lib -L/home/snagendra/Tools/json-c/lib -ljson-c -lcurl

ifeq ($(PREFIX),)
    PREFIX := /usr/local
endif

TARGET := libtest_network.so
TESTTARGET := test.o
SOURCES := $(shell echo *.c)
TESTSOURCES := $(shell echo test/*.c)
HEADERS := $(shell echo *.h)
OBJECTS := $(SOURCES:.c=.o)
CURDIR=$(shell pwd)

TESTCFLAGS := $(CFLAGS) -I$(CURDIR) -Itest
TESTLDFLAGS := -L/usr/local/lib -L/home/snagendra/Tools/json-c/lib -L$(CURDIR) -ljson-c -lcurl -ltest_network

all: $(TARGET)
	$(CC) $(CFLAGS) $(LDFLAGS) 

$(TARGET): $(OBJECTS)
	$(CC) $(CFLAGS) $(LDFLAGS) -o $(TARGET) $(OBJECTS)

test: $(TESTTARGET)
	./$(TESTTARGET)
	rm $(TESTTARGET)

$(TESTTARGET): $(TARGET)
	$(CC) $(TESTCFLAGS) $(TESTLDFLAGS) $(TESTSOURCES) -o $(TESTTARGET)

clean: 
	-@rm *.o $(TARGET)