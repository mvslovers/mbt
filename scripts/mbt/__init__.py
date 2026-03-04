"""mbt package constants."""

# Exit codes (spec section 11.1)
EXIT_SUCCESS    = 0   # success
EXIT_BUILD      = 1   # assembly/link failure
EXIT_CONFIG     = 2   # config/validation error
EXIT_DEPENDENCY = 3   # resolution/download failure
EXIT_MAINFRAME  = 4   # mvsMF communication error
EXIT_DATASET    = 5   # dataset operation failure
EXIT_INTERNAL   = 99  # unexpected exception
