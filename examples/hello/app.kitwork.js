import { app, env } from "kitwork";

app
  .root("app")
  .website({ hostname: "example.localhost", allowLocal: true, hotReload: true })
  .run(env.PORT || 8080);
