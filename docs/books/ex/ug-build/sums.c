#include <stdio.h>
#include <buildstamp.h>
#include "sums.h"

int main(void)
{
    static const int v[] = { 3, 5, 8, 13, 21 };

    printf("%s %s (%s)\n", MBT_PROJECT, MBT_VERSION, MBT_COMMIT);
    printf("sum %d, mean %d\n", addup(5, v), mean(5, v));
    return 0;
}
