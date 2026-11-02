$JOBCARD
//*********************************************************************
//* $PRODUCT $VERSION -- SMP4 ACCEPT, FMID $FMID
//*
//* SUBMIT THIS JOB WHEN YOU HAVE TRIED THE PRODUCT
//*   Run $INSTALL_JOB first: it ends after the APPLY.
//*   Submit this job after an install job whose APPLY CHECK and
//*   APPLY ended with return code 0, or with 4 for HMA2461 (a
//*   predecessor in delete) or HMA4441 (a library of SRC elements)
//*   alone: that is the normal end of an upgrade. Not after 8 or
//*   12: the GUARD below then stops the job.
//*   Until the ACCEPT a RESTORE can still take the function out
//*   again. ACCEPT is the point without a way back, so the decision
//*   is yours and the install job does not make it for you.
//*
//* WHAT THIS JOB DOES
//*   GUARD     look for $FMID in the target zone (CDS)
//*   ACCEPT    make this level the base a RESTORE returns to
//*   CLEANUP   scratch the staging $STAGING, now spent
//*
//* The GUARD is the check that the install job did its work. It
//* looks for the entry of $FMID in the target zone, not at the
//* return code of the install job. If $FMID is not applied (the
//* APPLY CHECK ended above 0, the install job never ran, or a
//* RESTORE took the function out), the job ends at return code 8 in
//* the step NOTAPPL, and the ACCEPT and the CLEANUP do not run.
//* Nothing is accepted, nothing is scratched.
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
