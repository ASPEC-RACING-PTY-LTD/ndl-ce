import Editor from "@monaco-editor/react";
import { useEffect } from "react";
import { ensureMonacoTheme } from "./FileEditor";

/** CodeEditor is a small Monaco editor for JSON and other code. Load it lazily. */
export default function CodeEditor({
  value,
  onChange,
  language = "json",
  height = "24rem",
  label,
}: {
  value: string;
  onChange: (next: string) => void;
  language?: string;
  height?: string;
  label: string;
}) {
  useEffect(() => {
    ensureMonacoTheme();
  }, []);
  return (
    <div className="code-editor" aria-label={label}>
      <Editor
        height={height}
        theme="ndl-dark"
        language={language}
        value={value}
        onChange={(next) => onChange(next ?? "")}
        options={{
          minimap: { enabled: false },
          fontSize: 13,
          lineNumbers: "on",
          automaticLayout: true,
          tabSize: 2,
          insertSpaces: true,
          scrollBeyondLastLine: false,
          wordWrap: "on",
          formatOnPaste: true,
        }}
      />
    </div>
  );
}
