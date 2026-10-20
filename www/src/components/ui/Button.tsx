import type { ButtonHTMLAttributes, ReactNode } from "react";
import { Icon, type IconName } from "./Icon";
import "./Button.scss";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
    variant?: "primary" | "glass" | "ghost" | "danger";
    size?: "sm" | "md";
    icon?: IconName;
    busy?: boolean;
    children?: ReactNode;
}

export function Button({ variant = "glass", size = "md", icon, busy, children, className = "", disabled, ...rest }: ButtonProps) {
    return (
        <button
            className={`sk-btn sk-btn--${variant} sk-btn--${size} ${busy ? "sk-btn--busy" : ""} ${className}`}
            disabled={disabled || busy}
            {...rest}
        >
            {icon && <Icon name={icon} size={size === "sm" ? 14 : 16} />}
            {children && <span>{children}</span>}
        </button>
    );
}
