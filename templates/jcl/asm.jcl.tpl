$JOBCARD
//*-----------------------------------------------------------
//* Assemble: $MEMBER
//*-----------------------------------------------------------
//ASM     EXEC PGM=IFOX00,
//          PARM='DECK,NOLOAD,TERM,XREF(SHORT)'
$SYSLIB_CONCAT
//SYSUT1   DD UNIT=SYSDA,SPACE=(CYL,(1,1))
//SYSUT2   DD UNIT=SYSDA,SPACE=(CYL,(1,1))
//SYSUT3   DD UNIT=SYSDA,SPACE=(CYL,(1,1))
//SYSPUNCH DD DSN=$PUNCH_DSN($MEMBER),DISP=SHR
//SYSTERM  DD SYSOUT=*
//SYSPRINT DD SYSOUT=*
//SYSGO    DD DUMMY
//SYSIN    DD *
$ASM_SOURCE
/*
//
