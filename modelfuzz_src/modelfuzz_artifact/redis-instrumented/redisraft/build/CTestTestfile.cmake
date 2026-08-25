# CMake generated Testfile for 
# Source directory: /home/snagendra/Fuzzing/redisraft-fuzzing
# Build directory: /home/snagendra/Fuzzing/redisraft-fuzzing/build
# 
# This file includes the relevant testing commands required for 
# testing this directory and lists subdirectories to be tested as well.
add_test(main "/home/snagendra/Fuzzing/redisraft-fuzzing/build/main")
set_tests_properties(main PROPERTIES  LABELS "redisraft-test" _BACKTRACE_TRIPLES "/home/snagendra/Fuzzing/redisraft-fuzzing/CMakeLists.txt;331;add_test;/home/snagendra/Fuzzing/redisraft-fuzzing/CMakeLists.txt;0;")
subdirs("deps/raft")
subdirs("deps/hiredis")
subdirs("deps/test_network")
