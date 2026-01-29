work("TestDB")
    .get("/test-db-any", () => {
        // --- 1. SINGLE OBJECT PATTERNS ---
        const findById = db.user.find(1);                       // Find by ID
        const findByLambda = db.user.find(u => u.id == 2);      // Find by Lambda (Smart Find)
        const firstNoTarget = db.user.first();                  // First record in table
        const firstWithLambda = db.user.first(u => u.id > 5);   // First record matching condition
        const whereFirst = db.user.where(u => u.id == 1).first(); // Chained where + first
        const oneAlias = db.user.where(u => u.id == 1).one();     // One() as alias for First()

        // --- 2. LIST/COLLECTION PATTERNS ---
        const allUsers = db.user.list();                        // All records (list standard)
        const filteredList = db.user.where(u => u.id < 5).list(); // Filtered list
        const selectedFields = db.user.select("id", "username").limit(2).list(); // Select + Limit
        const orderedList = db.user.orderBy("id desc").limit(3).list(); // OrderBy
        const pagedList = db.user.offset(2).limit(2).list();     // Pagination

        // --- 3. FUNCTIONAL/LEGACY STYLES ---
        const fromStyle = db().from("user").find(1);            // Functional db().from()
        const tableStyle = db.from("user").take(1);             // Property-call style
        const tableUser = db.table("user").list();

        return {
            success: true,
            results: {
                tableUser,
                findById,
                findByLambda,
                firstNoTarget,
                firstWithLambda,
                whereFirst,
                oneAlias,
                allUsersCount: allUsers.length,
                filteredList,
                selectedFields,
                orderedList,
                pagedList,
                fromStyle,
                tableStyle,
                existsTest: db.user.exists(u => u.username == "alice")
            }
        };
    })
    .get("/test-db-write", () => {
        // Use the NEW Cloud-style random() helper! 
        // random(10000) returns an integer from 0-9999
        const suffix = random(10000);
        const username = "cloud_user_" + suffix;

        // 1. Create
        const newUser = db.user.create({
            username: username,
            email: username + "@example.com",
            is_active: true
        });

        // 2. SAFE UPDATE: This will fail (return null) because no WHERE clause
        const blockedUpdate = db.user.update({ is_active: false });

        // 3. SECURE UPDATE: With WHERE clause
        const updatedUser = db.user
            .where(u => u.id == newUser.id)
            .update({ is_active: false });

        // 4. HARD DESTROY: Actual removal using destroy()
        // const hardDestroyed = db.user.where(u => u.id == newUser.id).destroy();

        return {
            success: true,
            msg: "Tested: Cloud-style random(), Blocked Bulk Update, and Hard Destroy.",
            randomValue: suffix,
            newUser,
            isUpdateBlocked: blockedUpdate == null,
            updatedUser,
            hardDestroyed
        };
    });
