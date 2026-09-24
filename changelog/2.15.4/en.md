## Fixes
- An app whose container does not stop within its stop timeout (for example, a Java service that ignores the stop signal) is now removed by force and reaches "Stopped", instead of being left in "Stop Failed" with logs that could not be opened.
- An app you stopped yourself that is stuck in "Stop Failed" now retries the stop on its own and reaches "Stopped"; it is not started again.
