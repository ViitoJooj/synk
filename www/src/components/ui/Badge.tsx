import type { ReactNode } from "react";
import "./Badge.scss";

export type Tone = "success" | "progress" | "pending" | "review" | "failed" | "idle";

export function Badge({ children, tone = "idle", dot }: { children: ReactNode; tone?: Tone; dot?: boolean }) {
    return (
        <span className={`sk-badge sk-badge--${tone}`}>
            {dot && <span className="sk-badge__dot" />}
            {children}
        </span>
    );
}
