work("view_demo")
    .router("GET", "/api/view/welcome");

// Simple HTML template with data injection
const template = `
    <div style="font-family: sans-serif; padding: 20px; color: #333;">
        <h1 style="color: #007bff;">Welcome, {{name}}!</h1>
        <p>This page was rendered using <strong>Kitwork Engine</strong> at {{time}}.</p>
        <hr/>
        <ul>
            <li>Core logic: Fast & Secure</li>
            <li>Database: Built-in Mock/Postgres</li>
            <li>Rendering: Automatic Template Engine</li>
        </ul>
    </div>
`;

return html(template, {
    name: "Developer",
    time: now().text()
});
