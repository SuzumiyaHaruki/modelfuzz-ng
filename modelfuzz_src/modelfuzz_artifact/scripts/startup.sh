#!/bin/bash

if [ -z "$(docker images -q modelfuzz:latest 2> /dev/null)" ]; then
  echo "Docker image does not exist. Make sure you have loaded/built it"
fi

docker run -it modelfuzz:latest /bin/bash
