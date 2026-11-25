cmake_minimum_required(VERSION 3.14)
project(runner LANGUAGES CXX)

include("${FLUTTER_MANAGED_DIR}/ephemeral/generated_config.cmake")
include("${CMAKE_CURRENT_SOURCE_DIR}/app_branding.cmake")
configure_file("app_branding.h.in" "${CMAKE_CURRENT_BINARY_DIR}/app_branding.h" @ONLY)

add_library(guo_window SHARED
  "flutter_window.cpp" "main.cpp" "utils.cpp" "win32_window.cpp"
  "${FLUTTER_MANAGED_DIR}/generated_plugin_registrant.cc"
  "${CMAKE_CURRENT_BINARY_DIR}/app_branding.h"
)
add_executable(${BINARY_NAME} WIN32 "dev_entry.cpp" "Runner.rc" "runner.exe.manifest")

foreach(target guo_window ${BINARY_NAME})
  apply_standard_settings(${target})
  target_compile_options(${target} PRIVATE /utf-8)
  target_compile_definitions(${target} PRIVATE "NOMINMAX"
    "FLUTTER_VERSION=\"${FLUTTER_VERSION}\""
    "FLUTTER_VERSION_MAJOR=${FLUTTER_VERSION_MAJOR}"
    "FLUTTER_VERSION_MINOR=${FLUTTER_VERSION_MINOR}"
    "FLUTTER_VERSION_PATCH=${FLUTTER_VERSION_PATCH}"
    "FLUTTER_VERSION_BUILD=${FLUTTER_VERSION_BUILD}")
  target_include_directories(${target} PRIVATE "${CMAKE_SOURCE_DIR}" "${CMAKE_CURRENT_BINARY_DIR}")
endforeach()
target_link_libraries(guo_window PRIVATE flutter flutter_wrapper_app dwmapi.lib shell32.lib)
target_link_libraries(${BINARY_NAME} PRIVATE guo_window)
add_dependencies(guo_window flutter_assemble)
