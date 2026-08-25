#!/bin/bash

cd /Fuzzing/tlc-controlled

ant -f customBuild.xml compile
ant -f customBuild.xml compile-test
ant -f customBuild.xml dist

