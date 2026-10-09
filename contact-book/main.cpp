#include <iostream>

int main() {
  int x = 10;
  void *p = &x;

  std::cout << *static_cast<int *>(p) << '\n';

  return 0;
}
