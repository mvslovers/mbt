/* hello.c - Hello World demonstrating the full mbt build stack.
 *
 * - wtof()   : console output via crent370
 * - greet()  : function from second C module (greet.c)
 * - mywto()  : assembler function from asm/mywto.asm
 */
#include <clibwto.h>

extern void greet(void);
extern void mywto(void);

int main(void)
{
    wtof("HELLO: Hello, World!\n");
    greet();
    mywto();
    return 0;
}
