#include <mbtcheck.h>

static int add(int a, int b) { return a + b; }

int main(void)
{
    CHECK_EQ(add(2, 3), 5, "2 + 3");
    CHECK(add(-1, 1) == 0, "-1 + 1 is 0");
    return mbt_test_summary("TSTADD");
}
