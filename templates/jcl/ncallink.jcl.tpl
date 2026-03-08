$JOBCARD
//*-----------------------------------------------------------
//* NCAL Link: $MEMBER
//*-----------------------------------------------------------
//LINK    EXEC PGM=IEWL,PARM='NCAL,LIST,XREF,LET,RENT'
//SYSUT1   DD UNIT=SYSDA,SPACE=(CYL,(1,1))
//SYSPRINT DD SYSOUT=*
//SYSLMOD  DD DSN=$NCALIB_DSN($MEMBER),DISP=SHR
//SYSLIN   DD DSN=$PUNCH_DSN($MEMBER),DISP=SHR
//
