$JOBCARD
//*********************************************************************
//* $PRODUCT $VERSION -- allocate the SMP4 installation datasets
//*
//* RUN THIS ONCE, BEFORE $INSTALL_JOB.
//*
//* There is deliberately no DELETE step. Once FMID $FMID has been
//* accepted, the distribution libraries hold the accepted copy of
//* every element; a re-run that scratched them would leave the SMP
//* inventory reporting "accepted" with an empty library behind it,
//* and nothing would say so. If you must start over, delete the
//* datasets by hand and reject the SYSMOD first.
//*
//* Space is in cylinders on purpose: a track primary reaches the
//* 16-extent limit long before the volume is full, and the SB37 that
//* follows reads like a full volume.
//*
//* Edit UNIT / VOL=SER below if SYSDA is not what you want.
//*********************************************************************
//ALLOC   EXEC PGM=IEFBR14
$ALLOC_DDS
//
