$JOBCARD
//*********************************************************************
//* $PRODUCT $VERSION -- SMP4 install, FMID $FMID
//*
//* BEFORE YOU SUBMIT
//*   1. run the allocation job once:
//*        $ALLOC_JOB
//*   2. upload the shipped .xmit files to MVS in BINARY
//*      (RECFM=FB, LRECL=80) -- any dataset names you like
//*   3. replace every $EDIT_PREFIX.* below with the names you used
//*      ------ those are the only lines you have to change ------
//*
//* WHAT THIS JOB DOES
$RECEIVE_SUMMARY
//*   RECV      receive SYSMOD $FMID into the SMP inventory
//*   APPLYCHK  dry run; APPLY only proceeds if this ends RC=0
//*   APPLY     copy the load modules into the target library
$ACCEPT_SUMMARY
//*
//* SMP handles the load modules only. The sample library is a plain
//* PDS restored by TSO RECEIVE and is none of SMP's business: the
//* product owns those patterns, the site owns the copies it makes of
//* them. See the README for which member goes where.
//*
//* The SYSMOD travels inline in this job -- it is plain text, so
//* there is no third file to upload and no dataset to allocate for
//* it. Measured on SMP 4 level 04.48: received RC 0, and the stored
//* PTS member came back byte-identical.
//*********************************************************************
//*
//* ---- unpack the shipped libraries ---------------------------------
$RECEIVE_STEPS
//*
//* ---- RECEIVE the SYSMOD -------------------------------------------
//* DD DATA, not DD *: the SYSMOD carries the JCLIN, whose cards start
//* with // in column 1, and a /* card closing the inline copy
//* statements. Either would cut a DD * stream short. DLM= moves the
//* terminator somewhere the SYSMOD never goes.
//*
//RECV    EXEC SMPREC,COND=(0,NE,$LAST_RECV)
//SMPPTFIN DD  DATA,DLM=$DELIM
$MCS
$DELIM
//SMPCNTL  DD  *
 RESETRC .
 RECEIVE SELECT($FMID) .
/*
//*
//* ---- APPLY CHECK: reports what would happen, changes nothing ------
//* NOTE ON THE DD ORDER: every DD that OVERRIDES one the SMPAPP
//* procedure already has must come before every DD that is ADDED to
//* the step. In the wrong order the override is not an override at
//* all -- both datasets are allocated under the same ddname, SMP
//* quietly uses the procedure's, and the messages still name the
//* ddname you wanted. Do not reorder them.
//*
//APPLYCHK EXEC SMPAPP,COND=(0,NE,RECV.HMASMP)
$APPLY_DDS
//SMPCNTL  DD  *
 APPLY S($FMID) CHECK .
/*
//*
//* ---- APPLY --------------------------------------------------------
//APPLY   EXEC SMPAPP,COND=(0,NE,APPLYCHK.HMASMP)
$APPLY_DDS
//SMPCNTL  DD  *
 APPLY S($FMID) DIS(WRITE) .
/*
$ACCEPT_STEP
