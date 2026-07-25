import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
// Token + font + base CSS load order matters: tokens define the custom
// properties everything else references.
import "./tokens.css";
import "./fonts.css";
import "./base.css";
import { App } from "./App";

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing #root element");

createRoot(rootEl).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
