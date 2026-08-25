#!/bin/bash

if [ -d "results" ]; then
    rm -rf results
fi

if [ -d "tmp" ]; then
    rm -rf tmp
fi

./redisraft-fuzzing compare -e 20000 --runs 3 -w 10
