#!/bin/bash

if [ -d "results" ]; then
    rm -rf results
fi

if [ -d "tmp" ]; then
    rm -rf tmp
fi

./redisraft-fuzzing compare -e 10 --runs 1
