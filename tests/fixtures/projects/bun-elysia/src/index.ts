import { Elysia } from "elysia";

new Elysia()
  .get("/health", () => "ok")
  .listen(3000);
