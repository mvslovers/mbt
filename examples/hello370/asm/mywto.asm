         TITLE 'MYWTO - WRITE TO OPERATOR FROM C'
***
***  MYWTO - C-callable assembler function.
***
***  Writes "HELLO FROM ASM" to the operator console
***  via SVC 35 (WTO) with an inline text block.
***
***  Calling convention (MVS standard):
***    R14 = return address
***    R15 = entry point
***    R13 = caller save area pointer
***    No parameters.
***
MYWTO    CSECT
         STM   14,12,12(13)      Save caller registers
         LR    12,15             Establish base register
         USING MYWTO,12
*
*        R1 -> WTO text block (length + flags + text)
*
         LA    1,WTOMSG          Point R1 to WTO parameter block
         SVC   35                WTO: write to operator console
*
         SR    15,15             RC = 0
         LM    14,12,12(13)      Restore caller registers
         BR    14                Return to caller
*
*        WTO text block: length, MCS flags, text
*
WTOMSG   DC    AL2(WTOEND-WTOMSG)  Total length of block
         DC    H'0'                MCS flags (default routing)
         DC    C'HELLO FROM ASM'
WTOEND   EQU   *
         LTORG
         END   MYWTO
