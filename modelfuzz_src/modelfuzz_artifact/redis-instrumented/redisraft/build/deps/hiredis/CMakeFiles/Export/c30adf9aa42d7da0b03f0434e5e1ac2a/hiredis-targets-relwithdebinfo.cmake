#----------------------------------------------------------------
# Generated CMake target import file for configuration "RelWithDebInfo".
#----------------------------------------------------------------

# Commands may need to know the format version.
set(CMAKE_IMPORT_FILE_VERSION 1)

# Import target "hiredis::hiredis" for configuration "RelWithDebInfo"
set_property(TARGET hiredis::hiredis APPEND PROPERTY IMPORTED_CONFIGURATIONS RELWITHDEBINFO)
set_target_properties(hiredis::hiredis PROPERTIES
  IMPORTED_LOCATION_RELWITHDEBINFO "${_IMPORT_PREFIX}/lib/libhiredis.so.1.0.0"
  IMPORTED_SONAME_RELWITHDEBINFO "libhiredis.so.1.0.0"
  )

list(APPEND _cmake_import_check_targets hiredis::hiredis )
list(APPEND _cmake_import_check_files_for_hiredis::hiredis "${_IMPORT_PREFIX}/lib/libhiredis.so.1.0.0" )

# Import target "hiredis::hiredis_static" for configuration "RelWithDebInfo"
set_property(TARGET hiredis::hiredis_static APPEND PROPERTY IMPORTED_CONFIGURATIONS RELWITHDEBINFO)
set_target_properties(hiredis::hiredis_static PROPERTIES
  IMPORTED_LINK_INTERFACE_LANGUAGES_RELWITHDEBINFO "C"
  IMPORTED_LOCATION_RELWITHDEBINFO "${_IMPORT_PREFIX}/lib/libhiredis_static.a"
  )

list(APPEND _cmake_import_check_targets hiredis::hiredis_static )
list(APPEND _cmake_import_check_files_for_hiredis::hiredis_static "${_IMPORT_PREFIX}/lib/libhiredis_static.a" )

# Commands beyond this point should not need to know the version.
set(CMAKE_IMPORT_FILE_VERSION)
