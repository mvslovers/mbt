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
//* and nothing would say so. After ACCEPT, REJECT no longer works
//* (HMA2462). To start over, cut the FMID out of the inventory with
//* UCLIN (see the product's removal guide), then delete the
//* datasets by hand.
//*
//* Space is in cylinders on purpose: a track primary reaches the
//* 16-extent limit long before the volume is full, and the SB37 that
//* follows reads like a full volume.
//*
//* Every library is allocated on a volume you name, never on whatever
//* SYSDA happens to pick:
//*
//*   TGTVOL  volume of the target libraries (the ones SMP installs
//*           into)                                      default $TGTVOL
//*   DLBVOL  volume of the distribution libraries (the ones ACCEPT
//*           fills)                                     default $DLBVOL
//*
//* The defaults are the volumes of an MVS/CE system. On TK5 write
//* TGTVOL=TK5RES and DLBVOL=TK5DLB, either on the PROC statement or on
//* the EXEC ALLOC statement below.
//*
//* A volume that is not mounted does not fail the job, it holds it:
//* the console shows IEF238D ... REPLY DEVICE NAME OR 'CANCEL' and the
//* job waits. Reply CANCEL, correct the volume, submit the job again.
//*********************************************************************
//SMPALLOC PROC TGTVOL=$TGTVOL,DLBVOL=$DLBVOL
//ALLOC   EXEC PGM=IEFBR14
$ALLOC_DDS
//        PEND
//ALLOC   EXEC SMPALLOC
//
