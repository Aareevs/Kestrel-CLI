import { Builder } from "fresh/dev";

const builder = new Builder();
await builder.listen(() => import("./main.ts"));
