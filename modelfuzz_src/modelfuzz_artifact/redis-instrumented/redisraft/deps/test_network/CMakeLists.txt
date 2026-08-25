CMAKE_MINIMUM_REQUIRED(VERSION 3.4.0)
PROJECT(test_network)

set(CMAKE_SHARED_MODULE_SUFFIX ".so")


# ------------------------------ Module library ---------------------------------#
add_library(test_network MODULE
    client.c
    ds.c
    http_client.c
    mongoose.c
    http_server.c
    types.c
)

set_target_properties(test_network PROPERTIES PREFIX "")
set_target_properties(test_network PROPERTIES
        LIBRARY_OUTPUT_DIRECTORY "${PROJECT_SOURCE_DIR}")
set_property(TARGET test_network APPEND PROPERTY
        ADDITIONAL_CLEAN_FILES ${LIBRARY_OUTPUT_DIRECTORY}/$<TARGET_FILE_NAME:test_network>)

target_compile_options(test_network PUBLIC -Wall -Wno-void-pointer-to-int-cast -Wno-int-to-void-pointer-cast -Wno-incompatible-pointer-types-discards-qualifiers -Wno-int-conversion)

target_link_directories(test_network PUBLIC /usr/local/lib)
target_link_directories(test_network PUBLIC /home/snagendra/Tools/json-c/lib)
target_link_libraries(test_network PUBLIC json-c curl)

target_include_directories(test_network PUBLIC
        /home/snagendra/Tools/json-c/include)
target_include_directories(test_network PUBLIC
        /usr/local/include)

# ------------------------------ Module library end -----------------------------#     

# ------------------------------ Static library ---------------------------------#

add_library(test_network_static STATIC
    client.c
    ds.c
    http_client.c
    mongoose.c
    http_server.c
    types.c
)

set_target_properties(test_network_static PROPERTIES PREFIX "")
set_target_properties(test_network_static PROPERTIES
        LIBRARY_OUTPUT_DIRECTORY "${PROJECT_SOURCE_DIR}")
set_property(TARGET test_network_static APPEND PROPERTY
        ADDITIONAL_CLEAN_FILES ${LIBRARY_OUTPUT_DIRECTORY}/$<TARGET_FILE_NAME:test_network>)

target_compile_options(test_network_static PUBLIC -Wall -Wno-void-pointer-to-int-cast -Wno-int-to-void-pointer-cast -Wno-incompatible-pointer-types-discards-qualifiers -Wno-int-conversion -fPIC)

target_link_directories(test_network_static PUBLIC /usr/local/lib)
target_link_directories(test_network_static PUBLIC /home/snagendra/Tools/json-c/lib)
target_link_libraries(test_network_static PUBLIC json-c curl)

target_include_directories(test_network_static PUBLIC
        /home/snagendra/Tools/json-c/include)
target_include_directories(test_network_static PUBLIC
        /usr/local/include)

# ------------------------------ Static library end -----------------------------#

# ------------------------------ Unit tests -------------------------------------#



# ------------------------------ Unit tests end ---------------------------------#