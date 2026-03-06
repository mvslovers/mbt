$JOBCARD
//*-----------------------------------------------------------
//* Full Linkedit: $MODULE_NAME
//*-----------------------------------------------------------
//LINK    EXEC PGM=IEWL,PARM='$LINK_OPTIONS'
//SYSUT1   DD UNIT=SYSDA,SPACE=(CYL,(5,2))
//SYSPRINT DD SYSOUT=*
//SYSLMOD  DD DSN=$SYSLMOD_DSN($MODULE_NAME),DISP=SHR
$SYSLIB_CONCAT
$NCALIB_CONCAT
//SYSLIN   DD *
$INCLUDE_STMTS
 ENTRY $ENTRY_POINT
 NAME $MODULE_NAME(R)
/*
//
