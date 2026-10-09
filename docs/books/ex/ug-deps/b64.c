#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <base64.h>

int main(void)
{
    const char *text = "MVS 3.8j";
    unsigned char *enc;

    enc = base64_encode((const unsigned char *)text, strlen(text), NULL);
    if (enc == NULL)
        return 8;
    printf("%s -> %s\n", text, enc);
    free(enc);
    return 0;
}
