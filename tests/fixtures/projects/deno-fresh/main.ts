import { App } from "fresh";

export const app = new App();
app.get("/health", () => new Response("ok"));
