$JOBCARD
//*********************************************************************
//* $PRODUCT $VERSION -- SMP4 ACCEPT, FMID $FMID
//*
//* SUBMIT THIS JOB WHEN YOU HAVE TRIED THE PRODUCT
//*   Run $INSTALL_JOB first: it ends after the APPLY.
//*   Until the ACCEPT a RESTORE can still take the function out
//*   again. ACCEPT is the point without a way back, so the decision
//*   is yours and the install job does not make it for you.
//*
//* WHAT THIS JOB DOES
//*   GUARD     look for $FMID in the target zone (CDS)
//*   ACCEPT    make this level the base a RESTORE returns to
//*   CLEANUP   scratch the staging $STAGING, now spent
//*
//* The GUARD is the check that the install job did its work: if
//* $FMID is not applied (the APPLY CHECK ended above 0, the
//* install job never ran, or a RESTORE took the function out), the
//* job ends at return code 8 in the step NOTAPPL, and the ACCEPT and
//* the CLEANUP do not run. Nothing is accepted, nothing is scratched.
//*
//* Nothing in this job needs editing: it names the same datasets as
//* the install job, and the ACCEPT reads the shipped members from
//* the staging $STAGING again, which is why CLEANUP waits for it.
//* If the ACCEPT fails, CLEANUP does not run and nothing is scratched:
//* fix the cause and submit the job again.
//*$BYPASS_NOTE
//*********************************************************************
$GUARD_STEP
$ACCEPT_STEP
$CLEANUP_STEP
