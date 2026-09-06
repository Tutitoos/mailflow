import { cva, type VariantProps } from "class-variance-authority";
import type { ButtonHTMLAttributes } from "react";
import { cn } from "../../lib/utils";

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white disabled:pointer-events-none disabled:opacity-50 active:scale-[0.98]",
  {
    variants: {
      variant: {
        primary: "bg-white text-black hover:bg-zinc-200",
        ghost: "text-zinc-300 hover:bg-zinc-800 hover:text-white",
        outline: "border border-zinc-700 bg-black text-zinc-200 hover:border-zinc-500",
      },
      size: {
        default: "h-10 px-4",
        icon: "h-10 w-10",
        sm: "h-8 px-3 text-[13px]",
      },
    },
    defaultVariants: {
      variant: "ghost",
      size: "default",
    },
  },
);

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> &
  VariantProps<typeof buttonVariants>;

export function Button({ className, variant, size, type = "button", ...props }: ButtonProps) {
  return (
    <button type={type} className={cn(buttonVariants({ variant, size }), className)} {...props} />
  );
}
