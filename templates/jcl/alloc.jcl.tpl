$JOBCARD
//*-----------------------------------------------------------
//* Allocate dataset: $DSN
//*-----------------------------------------------------------
//ALLOC   EXEC PGM=IEFBR14
//DD1     DD DSN=$DSN,
//           DISP=(NEW,CATLG,DELETE),
//           UNIT=$UNIT,
//           SPACE=($SPACE_UNIT,($SPACE_PRI,$SPACE_SEC,$SPACE_DIR)),
//           DCB=(DSORG=$DSORG,RECFM=$RECFM,LRECL=$LRECL,BLKSIZE=$BLKSIZE)
//
