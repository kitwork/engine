work("SleekScheduler")
    .handle(() => {
        log("Atomic Pulse: Executing logic");
    })
    // .hourly(0, 30, "15", "45")      // Minutes as numbers and strings
    // .daily("13:00", "01:00")        // Variadic times
    // .weekly("MONDAY 08:30")         // Specific start of week
    // .monthly("1st")                 // Just the day
    .every("5s");                   // Interval
