import type { ReactNode } from "react";

type EditorProps = {
  value?: string;
  language?: string;
  onChange?: (value: string | undefined) => void;
  options?: { readOnly?: boolean };
};

export const loader = {
  config() {},
};

export default function Editor(props: EditorProps): ReactNode {
  return (
    <textarea
      data-testid="monaco"
      aria-label="File contents"
      readOnly={props.options?.readOnly}
      value={props.value}
      onChange={(event) => props.onChange?.(event.target.value)}
    />
  );
}
