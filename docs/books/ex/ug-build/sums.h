#ifndef SUMS_H
#define SUMS_H

int addup(int n, const int *v);   /* asm/addup.asm: v[0] + ... + v[n-1] */
int mean(int n, const int *v);    /* src/mean.c: the mean, rounded down */

#endif
