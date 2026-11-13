#include <windows.h>

extern "C" __declspec(dllimport) int WINAPI GuoRunWindow();

int APIENTRY wWinMain(HINSTANCE, HINSTANCE, wchar_t*, int) {
  return GuoRunWindow();
}
