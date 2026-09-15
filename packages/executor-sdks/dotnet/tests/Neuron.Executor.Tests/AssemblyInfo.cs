using Xunit;

// Serialize the test assembly because all test classes mutate process-level
// environment variables that must not race across collections.
[assembly: CollectionBehavior(DisableTestParallelization = true)]