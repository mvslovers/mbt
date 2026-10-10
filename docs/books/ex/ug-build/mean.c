#include "sums.h"

int mean(int n, const int *v)
{
    return n > 0 ? addup(n, v) / n : 0;
}
