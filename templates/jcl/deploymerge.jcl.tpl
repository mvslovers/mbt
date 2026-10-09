$JOBCARD
//*-----------------------------------------------------------
//* DEPLOY: $XMIT_DSN -> $STAGE_DSN -> $TARGET_DSN
//*-----------------------------------------------------------
//* The target is never deleted: a running server holds it
//* DISP=SHR, and so does the copy step (mbt#105). RECEIVE
//* fills a staging load library of this project's own; IEBCOPY
//* replaces the members in the target from it. REGION as in
//* receive.jcl.tpl (IEBCOPY needs the buffer storage).
//*-----------------------------------------------------------
$ALLOC_STEP//RECV    EXEC PGM=IKJEFT01,REGION=4096K
//SYSTSPRT DD SYSOUT=*
//SYSTSIN  DD *
$RECEIVE_CMD
/*
//MERGE   EXEC PGM=IEBCOPY,REGION=4096K,COND=(4,LT,RECV)
//SYSPRINT DD SYSOUT=*
//STAGE    DD DISP=SHR,DSN=$STAGE_DSN
//TARGET   DD DISP=SHR,DSN=$TARGET_DSN
//SYSUT3   DD UNIT=SYSDA,SPACE=(TRK,(5,5))
//SYSUT4   DD UNIT=SYSDA,SPACE=(TRK,(5,5))
//SYSIN    DD *
  COPY OUTDD=TARGET,INDD=((STAGE,R))
/*
//
