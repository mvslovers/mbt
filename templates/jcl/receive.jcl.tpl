$JOBCARD
//*-----------------------------------------------------------
//* TSO RECEIVE: $XMIT_DSN -> $TARGET_DSN
//*-----------------------------------------------------------
//* REGION is explicit: the class default (512K on many systems)
//* leaves IEBCOPY, called by RECEIVE, too little buffer storage
//* once the TMP has set itself up -- IEB135I, RC 08, nothing
//* received. 4096K is measured-good; peak use was 1536K.
//*-----------------------------------------------------------
//RECV    EXEC PGM=IKJEFT01,REGION=4096K
//SYSTSPRT DD SYSOUT=*
//SYSTSIN  DD *
$RECEIVE_CMD
/*
//
