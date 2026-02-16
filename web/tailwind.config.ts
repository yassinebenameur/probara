import type { Config } from "tailwindcss";

const config: Config = {
  content: [
    "./pages/**/*.{js,ts,jsx,tsx,mdx}",
    "./components/**/*.{js,ts,jsx,tsx,mdx}",
    "./app/**/*.{js,ts,jsx,tsx,mdx}",
  ],
  theme: {
    extend: {
      colors: {
        bg: "#050711",
        "bg-elevated": "#090c16",
        surface: "#111525",
        "surface-alt": "#181c2e",
        accent: {
          DEFAULT: "#4f46e5",
          soft: "rgba(79, 70, 229, 0.12)",
          "soft-strong": "rgba(79, 70, 229, 0.2)",
        },
        primary: {
          DEFAULT: "#4f46e5",
          dark: "#4338ca",
        },
        secondary: {
          DEFAULT: "#64748b",
          dark: "#475569",
        },
        success: {
          DEFAULT: "#22c55e",
          dark: "#16a34a",
        },
        error: {
          DEFAULT: "#ef4444",
          dark: "#dc2626",
        },
        danger: "#ef4444",
        warning: "#facc15",
        muted: "#9ca3af",
      },
      borderRadius: {
        lg: "18px",
        xl: "22px",
        full: "999px",
      },
      boxShadow: {
        soft: "0 18px 45px rgba(0, 0, 0, 0.5)",
        subtle: "0 10px 30px rgba(0, 0, 0, 0.4)",
      },
    },
  },
  plugins: [],
};
export default config;

