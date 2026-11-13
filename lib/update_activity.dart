class UpdateActivity {
  static int _players = 0;
  static bool get playing => _players > 0;
  static void enterPlayer() => _players++;
  static void leavePlayer() {
    if (_players > 0) _players--;
  }
}
