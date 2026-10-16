import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import "./styles.css";
import "./content.css";
import "./design.css";
import { startThemeSync } from "./theme";

// The theme is cached in this browser, so it is applied before the first paint.
startThemeSync();

const root = document.getElementById("root");
if (!root) {
  throw new Error("root element missing");
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
