work("test_pass")
    .router("GET", "/api/test/pass");

// This endpoint is used to verify engine sanity
const checks = {
    math: (1 + 2) == 3,
    string: ("a" + "b") == "ab",
    logic: (true && !false) == true,
    array: [1, 2].len() == 2
};

return {
    status: "pass",
    timestamp: now(),
    checks: checks
};
