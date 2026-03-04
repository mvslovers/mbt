$JOBCARD
//*-----------------------------------------------------------
//* TSO RECEIVE: $XMIT_DSN -> $TARGET_DSN
//*-----------------------------------------------------------
//RECV    EXEC PGM=IKJEFT01
//SYSTSPRT DD SYSOUT=*
//SYSTSIN  DD *
$RECEIVE_CMD
/*
//
