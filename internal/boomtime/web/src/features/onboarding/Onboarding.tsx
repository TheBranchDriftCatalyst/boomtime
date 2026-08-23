import { useState } from "react";
import { useForm } from "react-hook-form";
import { Navigate } from "react-router";
import { z } from "zod";
import { Boxes, Download, Github, Layers, Wand2 } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import { useAuth } from "@shared/features/auth/useAuth";
import { usePublicConfig } from "@shared/lib/usePublicConfig";
import { useBetaRegistration } from "@shared/features/onboarding/betaRegistration";
import { OnboardingBackdrop } from "@boomtime/features/onboarding/OnboardingBackdrop";
import { WhyStep } from "@boomtime/features/onboarding/OnboardingWhy";
import { ApiError } from "@shared/lib/api";

// Routed beta onboarding flow (boom-93f.1.2): welcome -> what-is-boomtime demo
// -> signup. Reached by the RootLayout gate when the beta preview flag is
// active (?enable_beta_user_registration=true), including for an already-
// logged-in user so the new UX can be walked without logging out.
//
// The signup step branches on the server auth provider: "oidc" shows the
// "Continue with Authentik" CTA (wired to the OIDC login start, which lands in
// a later phase); "local" shows the username/password form against today's
// working POST /auth/register.

// The feature bullets shown on the demo step. Kept in sync with the copy in
// WelcomeModal (the localStorage first-run modal) so the two onboarding
// surfaces tell the same story.
const FEATURES = [
  {
    icon: Download,
    title: "Import history",
    body: "Pull your existing Wakatime data by date range — a first-class migration path, not an afterthought.",
  },
  {
    icon: Wand2,
    title: "Curation",
    body: "Rename or hide projects, languages, and machines to keep the view honest.",
  },
  {
    icon: Boxes,
    title: "Spaces",
    body: "Named, rule-based scopes — group work by client, product, or whatever axis matters.",
  },
  {
    icon: Layers,
    title: "Widgets",
    body: "Embed compact stat cards in READMEs and dashboards. Scope them per space.",
  },
];

const signupSchema = z
  .object({
    username: z.string().min(1, "Username is required"),
    password: z.string().min(8, "The password is too short (minimum 8 characters)"),
    confirmPassword: z.string(),
  })
  .refine((v) => v.password === v.confirmPassword, {
    message: "The passwords do not match",
    path: ["confirmPassword"],
  });
type SignupValues = z.infer<typeof signupSchema>;

type Step = "welcome" | "why" | "demo" | "signup";

export function Onboarding() {
  const { active, exit } = useBetaRegistration();
  const { config } = usePublicConfig();
  const [step, setStep] = useState<Step>("welcome");

  // Leaving the preview is a single path: clear the flag. That flips `active`
  // false, this component re-renders to <Navigate to="/">, and RootRedirect
  // sends a logged-in user (incl. one who just registered) to /app and an
  // anonymous visitor to /login. No imperative navigate() races the gate.
  if (!active) {
    return <Navigate to="/" replace />;
  }

  return (
    <div className="relative flex min-h-screen items-center justify-center overflow-x-hidden bg-muted/30 p-4 py-10">
      <OnboardingBackdrop />
      <div className="relative z-10 w-full max-w-lg space-y-3">
        {/* Preview banner — makes it unmistakable this is the beta flow, not
            the real signup, especially since the viewer may be logged in. */}
        <div className="flex items-center justify-between rounded-md border border-primary/40 bg-primary/10 px-3 py-2 text-xs">
          <span className="font-medium text-primary">
            Beta preview · new registration &amp; onboarding flow
          </span>
          <button
            type="button"
            onClick={exit}
            className="text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
          >
            Exit preview
          </button>
        </div>

        <Card className="w-full">
          <CardContent className="pt-6">
            {step === "welcome" && (
              <WelcomeStep onNext={() => setStep("why")} />
            )}
            {step === "why" && (
              <WhyStep
                onBack={() => setStep("welcome")}
                onNext={() => setStep("demo")}
              />
            )}
            {step === "demo" && (
              <DemoStep
                onBack={() => setStep("why")}
                onNext={() => setStep("signup")}
              />
            )}
            {step === "signup" && (
              <SignupStep
                oidcEnabled={config.oidc_enabled}
                registrationEnabled={config.registration_enabled}
                onBack={() => setStep("demo")}
                onDone={exit}
              />
            )}
          </CardContent>
        </Card>

        <StepDots step={step} />
      </div>
    </div>
  );
}

function StepDots({ step }: { step: Step }) {
  const order: Step[] = ["welcome", "why", "demo", "signup"];
  return (
    <div className="flex justify-center gap-1.5">
      {order.map((s) => (
        <span
          key={s}
          aria-hidden
          className={
            "h-1.5 w-1.5 rounded-full " +
            (s === step ? "bg-primary" : "bg-muted-foreground/30")
          }
        />
      ))}
    </div>
  );
}

function WelcomeStep({ onNext }: { onNext: () => void }) {
  return (
    <div className="flex flex-col items-center gap-4 text-center">
      <img src="/boomtime.svg" alt="Boomtime" className="h-14 w-14 rounded-2xl" />
      <div className="space-y-1.5">
        <h1 className="text-2xl font-semibold">Welcome to Boomtime</h1>
        <p className="text-sm text-muted-foreground">
          A self-hosted, Wakatime-compatible coding-time tracker. Point your
          editor plugin at this server and your keystrokes turn into dashboards.
        </p>
      </div>
      <Button className="w-full" onClick={onNext}>
        Take the tour
      </Button>
    </div>
  );
}

function DemoStep({ onBack, onNext }: { onBack: () => void; onNext: () => void }) {
  return (
    <div className="space-y-5">
      <div className="space-y-1 text-center">
        <h2 className="text-lg font-semibold">What you can do</h2>
        <p className="text-sm text-muted-foreground">
          The essentials, in one minute.
        </p>
      </div>
      <ul className="space-y-3 text-sm">
        {FEATURES.map((f) => (
          <li key={f.title} className="flex gap-3">
            <f.icon className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
            <div>
              <div className="font-medium">{f.title}</div>
              <div className="text-muted-foreground">{f.body}</div>
            </div>
          </li>
        ))}
      </ul>
      <div className="flex gap-2">
        <Button variant="secondary" className="flex-1" onClick={onBack}>
          Back
        </Button>
        <Button className="flex-1" onClick={onNext}>
          Create your account
        </Button>
      </div>
    </div>
  );
}

function SignupStep({
  oidcEnabled,
  registrationEnabled,
  onBack,
  onDone,
}: {
  oidcEnabled: boolean;
  registrationEnabled: boolean;
  onBack: () => void;
  onDone: () => void;
}) {
  const { register: registerUser } = useAuth();
  const [error, setError] = useState("");
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<SignupValues>();

  async function onSubmit(values: SignupValues) {
    const parsed = signupSchema.safeParse(values);
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Invalid input");
      return;
    }
    try {
      await registerUser({
        username: parsed.data.username,
        password: parsed.data.password,
      });
      setError("");
      onDone();
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : "Unknown error";
      setError(`Registration failed: ${msg}`);
    }
  }

  return (
    <div className="space-y-4">
      <div className="space-y-1 text-center">
        <h2 className="text-lg font-semibold">Create your account</h2>
        <p className="text-sm text-muted-foreground">
          One account, all your coding stats.
        </p>
      </div>

      {/* OIDC path (lands in a later phase). When the server reports the
          Authentik provider, this is the primary CTA; local register is
          hidden because signup flows through Authentik. */}
      {oidcEnabled ? (
        <div className="space-y-3">
          <Button
            className="w-full"
            onClick={() => {
              window.location.href = "/auth/login/oidc?return_to=/app";
            }}
          >
            <Github className="mr-2 h-4 w-4" />
            Continue with Authentik
          </Button>
          <p className="text-center text-xs text-muted-foreground">
            You'll sign in (or sign up) through the identity provider.
          </p>
        </div>
      ) : registrationEnabled ? (
        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="ob-username">Username</Label>
            <Input id="ob-username" autoFocus {...register("username")} />
            {errors.username && (
              <p className="text-xs text-destructive">{errors.username.message}</p>
            )}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="ob-password">Password</Label>
            <Input id="ob-password" type="password" {...register("password")} />
            {errors.password && (
              <p className="text-xs text-destructive">{errors.password.message}</p>
            )}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="ob-confirm">Confirm password</Label>
            <Input id="ob-confirm" type="password" {...register("confirmPassword")} />
            {errors.confirmPassword && (
              <p className="text-xs text-destructive">
                {errors.confirmPassword.message}
              </p>
            )}
          </div>
          <div className="flex gap-2">
            <Button
              type="button"
              variant="secondary"
              className="flex-1"
              onClick={onBack}
            >
              Back
            </Button>
            <Button type="submit" className="flex-1" disabled={isSubmitting}>
              {isSubmitting ? "Creating…" : "Finish"}
            </Button>
          </div>
          {error && (
            <p className="text-center text-sm text-destructive">{error}</p>
          )}
        </form>
      ) : (
        <div className="space-y-4">
          <p className="text-center text-sm text-muted-foreground">
            Registration is currently disabled on this server. Ask an
            administrator to create an account for you.
          </p>
          <Button variant="secondary" className="w-full" onClick={onBack}>
            Back
          </Button>
        </div>
      )}
    </div>
  );
}
