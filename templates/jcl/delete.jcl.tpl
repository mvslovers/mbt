$JOBCARD
//*-----------------------------------------------------------
//* Delete dataset: $DSN
//*-----------------------------------------------------------
//DELETE  EXEC PGM=IEFBR14
//DD1     DD DSN=$DSN,
//           DISP=(OLD,DELETE,DELETE)
//
