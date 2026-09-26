import type { ReactNode } from "react";
import "./GlassCard.scss";

export function GlassCard({ children, className = "" }: { children: ReactNode; className?: string }) {
    return <section className={`sk-card ${className}`}>{children}</section>;
}
