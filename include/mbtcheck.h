/* mbtcheck.h -- minimal test convention for mbt projects.
 *
 * The mbt test runner's only hard contract is the RETURN CODE: a [[test]]
 * program returns 0 when every check passed and nonzero when any failed.
 * This header is the recommended (optional) way to produce that RC and the
 * uniform "  PASS:" / "  FAIL:" lines the runner counts for an aggregate tally.
 *
 * Usage (one test = one translation unit with one main()):
 *
 *     #include <mbtcheck.h>
 *
 *     int main(void)
 *     {
 *         printf("=== MYPROJ widget tests ===\n");
 *         CHECK(widget_init() == 0, "widget_init returns 0");
 *         CHECK_EQ(widget_count(), 3, "three widgets");
 *         return mbt_test_summary("TSTWIDG");   // RC 0 ok / 1 failed
 *     }
 *
 * Portable C89: builds with cc370 (MVS) and a host compiler (gcc/clang), so a
 * test source can run both as a native host unit test and as an MVS load
 * module.  Character literals are EBCDIC-correct under cc370; the macros use no
 * hardcoded character codes.
 *
 * The counters are file-static -- legitimate here (a test is a single TU with
 * its own main()); the no-global-state rule applies to production code, not the
 * test harness.
 */
#ifndef MBTCHECK_H
#define MBTCHECK_H

#include <stdio.h>

static int mbt_run = 0;
static int mbt_passed = 0;
static int mbt_failed = 0;

/* Record one assertion. Prints exactly one PASS:/FAIL: line (the runner counts
 * these) and updates the counters. `msg` is a plain description. */
#define CHECK(cond, msg)                       \
    do {                                       \
        mbt_run++;                             \
        if (cond) {                            \
            mbt_passed++;                      \
            printf("  PASS: %s\n", (msg));     \
        } else {                               \
            mbt_failed++;                      \
            printf("  FAIL: %s\n", (msg));     \
        }                                      \
    } while (0)

/* Convenience: assert two ints are equal, printing the values on failure. */
#define CHECK_EQ(got, want, msg)                                       \
    do {                                                               \
        long mbt_g = (long)(got), mbt_w = (long)(want);               \
        mbt_run++;                                                     \
        if (mbt_g == mbt_w) {                                          \
            mbt_passed++;                                              \
            printf("  PASS: %s\n", (msg));                            \
        } else {                                                       \
            mbt_failed++;                                              \
            printf("  FAIL: %s (got %ld, want %ld)\n",               \
                   (msg), mbt_g, mbt_w);                              \
        }                                                             \
    } while (0)

/* Print the standard summary line and return the process RC: 0 if every check
 * passed, 1 otherwise. Call as `return mbt_test_summary("TSTNAME");`. */
static int mbt_test_summary(const char *name)
{
    printf("\n=== %s: %d/%d passed", name, mbt_passed, mbt_run);
    if (mbt_failed > 0)
    {
        printf(" (%d FAILED)", mbt_failed);
    }
    printf(" ===\n");
    return mbt_failed > 0 ? 1 : 0;
}

#endif /* MBTCHECK_H */
