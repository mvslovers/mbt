*        int addup(int n, const int *v) -- sum of v[0] .. v[n-1]
ADDUP    CSECT
         STM   14,12,12(13)       save the caller's registers
         LR    12,15              R12 = base register
         USING ADDUP,12
         LM    2,3,0(1)           R2 = n, R3 = v
         SR    15,15              R15 = sum = 0
         LTR   2,2                no numbers?
         BNP   DONE
LOOP     A     15,0(,3)           add v[i]
         LA    3,4(,3)            next element
         BCT   2,LOOP
DONE     L     14,12(,13)         restore the caller's registers,
         LM    0,12,20(13)        except R15, the result
         BR    14
         END
