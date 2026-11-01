$JOBCARD
//*********************************************************************
//* $PRODUCT $VERSION -- SMP4 ACCEPT, FMID $FMID
//*
//* SUBMIT THIS JOB WHEN YOU HAVE TRIED THE PRODUCT
//*   Run $INSTALL_JOB first: it ends after the APPLY.
//*   Submit this job only if that job ended with return code 0:
//*   this job does not look at what the install job did.
//*   Until the ACCEPT a RESTORE can still take the function out
//*   again. ACCEPT is the point without a way back, so the decision
//*   is yours and the install job does not make it for you.
//*
//* WHAT THIS JOB DOES
//*   ACCEPT    make this level the base a RESTORE returns to
//*   CLEANUP   scratch the staging $STAGING, now spent
//*
//* Nothing in this job needs editing: it names the same datasets as
//* the install job, and the ACCEPT reads the shipped members from
//* the staging $STAGING again, which is why CLEANUP waits for it.
//* If the ACCEPT fails, CLEANUP does not run and nothing is scratched:
//* fix the cause and submit the job again.
//*$BYPASS_NOTE
//*********************************************************************
$ACCEPT_STEP
$CLEANUP_STEP
