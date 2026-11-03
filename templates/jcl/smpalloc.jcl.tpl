$JOBCARD
//*********************************************************************
//* $PRODUCT $VERSION -- allocate the SMP4 installation datasets
//*
//* RUN THIS ONCE, BEFORE $INSTALL_JOB.
//*
//* The step CHECK looks in the catalog first. On a first run it prints
//* IDC3012I ENTRY ... NOT FOUND for every library below: that is the
//* answer wanted, and CHECK ends RC 0. If a library is already
//* cataloged, CHECK lists it and ends RC 8, ALLOC does not run and
//* nothing is allocated. Without that look a second run would end RC 0
//* with IEF287I ... NOT CATLGD 2 and leave an uncataloged copy of the
//* library on the other volume.
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
//* An IEAAPF00 entry is "dsname volser". If the system already has
//* an entry for a library of this product, set TGTVOL to the volume
//* of that entry. A library of the same name on another volume
//* exists, holds the modules, and is not APF-authorized. This job
//* does not read the list and does not say if the volumes differ.
//*
//* A volume that is not mounted does not fail the job, it holds it:
//* the console shows IEF238D ... REPLY DEVICE NAME OR 'CANCEL' and the
//* job waits. Reply CANCEL, correct the volume, submit the job again.
//*********************************************************************
//SMPALLOC PROC TGTVOL=$TGTVOL,DLBVOL=$DLBVOL
//ALLOC   EXEC PGM=IEFBR14
$ALLOC_DDS
//        PEND
//CHECK   EXEC PGM=IDCAMS
//SYSPRINT DD  SYSOUT=*
//SYSIN    DD  *
$CHECK_CARDS
/*
//ALLOC   EXEC SMPALLOC,COND=(0,NE,CHECK)
//
