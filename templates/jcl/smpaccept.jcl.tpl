$JOBCARD
//*********************************************************************
//* $PRODUCT $VERSION -- SMP4 ACCEPT, FMID $FMID
//*
//* SUBMIT THIS JOB WHEN YOU HAVE TRIED THE PRODUCT
//*   Run $INSTALL_JOB first: it ends after the APPLY.
//*   ACCEPT is the point without a way back, so the decision is
//*   yours and the install job does not make it for you. Until then
//*   a RESTORE takes the load modules out again, but SMP4 fails it
//*   for macros (HMA4092): the distribution library is still empty.
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
