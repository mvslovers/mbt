$JOBCARD
//*-----------------------------------------------------------
//* Copy: $SRC_DSN -> $DST_DSN
//*-----------------------------------------------------------
//COPY    EXEC PGM=IEBCOPY
//SYSPRINT DD SYSOUT=*
//SYSUT3   DD UNIT=SYSDA,SPACE=(CYL,(1,1))
//INDD     DD DSN=$SRC_DSN,DISP=SHR
//OUTDD    DD DSN=$DST_DSN,DISP=SHR
//SYSIN    DD *
 COPY INDD=INDD,OUTDD=OUTDD
/*
//
