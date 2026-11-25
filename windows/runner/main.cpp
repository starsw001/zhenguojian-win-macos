#include <flutter/dart_project.h>
#include <flutter/flutter_view_controller.h>
#include <windows.h>
#include <cstdlib>
#include <cstring>
#include <cwctype>
#include <string>
#include <vector>

#include "flutter_window.h"
#include "utils.h"
#include "app_branding.h"

extern "C" __declspec(dllexport) unsigned WINAPI GuoCheckWindowHost(
    const char* executable, const char* version) {
  return std::strcmp(executable, APP_EXECUTABLE_NAME) == 0 &&
         std::strcmp(version, FLUTTER_VERSION) == 0;
}

namespace {

bool HasNoProxyEntry(const std::wstring& value, const std::wstring& expected) {
  size_t start = 0;
  while (start <= value.size()) {
    const size_t end = value.find(L',', start);
    std::wstring entry = value.substr(
        start, end == std::wstring::npos ? std::wstring::npos : end - start);
    while (!entry.empty() && std::iswspace(entry.front())) {
      entry.erase(entry.begin());
    }
    while (!entry.empty() && std::iswspace(entry.back())) {
      entry.pop_back();
    }
    if (_wcsicmp(entry.c_str(), expected.c_str()) == 0) {
      return true;
    }
    if (end == std::wstring::npos) {
      break;
    }
    start = end + 1;
  }
  return false;
}

std::wstring LocalMediaNoProxy() {
  std::wstring value;
  const DWORD length = ::GetEnvironmentVariableW(L"no_proxy", nullptr, 0);
  if (length > 0) {
    std::vector<wchar_t> buffer(length);
    const DWORD copied =
        ::GetEnvironmentVariableW(L"no_proxy", buffer.data(), length);
    if (copied > 0 && copied < length) {
      value.assign(buffer.data(), copied);
    }
  }
  for (const std::wstring entry : {L"127.0.0.1", L"localhost", L"::1"}) {
    if (!HasNoProxyEntry(value, entry)) {
      if (!value.empty() && value.back() != L',') {
        value.push_back(L',');
      }
      value.append(entry);
    }
  }
  return value;
}

}

extern "C" __declspec(dllexport) unsigned WINAPI
GuoConfigureLocalMediaProxyBypass() {
  const std::wstring value = LocalMediaNoProxy();
  const BOOL environmentUpdated =
      ::SetEnvironmentVariableW(L"no_proxy", value.c_str());
  const errno_t runtimeUpdated = _wputenv_s(L"no_proxy", value.c_str());
  return environmentUpdated && runtimeUpdated == 0;
}

extern "C" __declspec(dllexport) int WINAPI GuoRunWindow() {
  GuoConfigureLocalMediaProxyBypass();

  // Attach to console when present (e.g., 'flutter run') or create a
  // new console when running with a debugger.
  if (!::AttachConsole(ATTACH_PARENT_PROCESS) && ::IsDebuggerPresent()) {
    CreateAndAttachConsole();
  }

  // Initialize COM, so that it is available for use in the library and/or
  // plugins.
  ::CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED);

  flutter::DartProject project(L"data");

  std::vector<std::string> command_line_arguments =
      GetCommandLineArguments();

  project.set_dart_entrypoint_arguments(std::move(command_line_arguments));

  FlutterWindow window(project);
  Win32Window::Point origin(10, 10);
  Win32Window::Size size(1280, 720);
  if (!window.Create(APP_WINDOW_TITLE, origin, size)) {
    return EXIT_FAILURE;
  }
  window.SetCloseToTray(true);
  window.SetQuitOnClose(true);

  ::MSG msg;
  while (::GetMessage(&msg, nullptr, 0, 0)) {
    ::TranslateMessage(&msg);
    ::DispatchMessage(&msg);
  }

  ::CoUninitialize();
  return EXIT_SUCCESS;
}
