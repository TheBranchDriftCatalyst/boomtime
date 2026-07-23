import { useState } from "react";
import { useForm } from "react-hook-form";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Eye, EyeOff } from "lucide-react";
import { z } from "zod";
import { api, ApiError } from "@/lib/api";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";

// Zod schema: same client-side floor as the server (min 8, letter + digit).
// The server re-checks; this just gives instant feedback before the round-trip.
// The refine keeps the "match" error attached to confirmNewPassword so it
// renders under the correct field.
const schema = z
  .object({
    currentPassword: z.string().min(1, "Current password is required"),
    newPassword: z
      .string()
      .min(8, "The new password is too short (minimum 8 characters)")
      .regex(/[a-zA-Z]/, "The new password must contain a letter")
      .regex(/[0-9]/, "The new password must contain a digit"),
    confirmNewPassword: z.string(),
  })
  .refine((v) => v.newPassword === v.confirmNewPassword, {
    message: "The new passwords do not match",
    path: ["confirmNewPassword"],
  });
type FormValues = z.infer<typeof schema>;

// Small show/hide toggle. Keeping it inline rather than lifting to a shared
// component — only Profile uses it today and Radix Input doesn't ship one.
function PasswordField({
  id,
  register,
  autoFocus,
}: {
  id: keyof FormValues;
  register: ReturnType<typeof useForm<FormValues>>["register"];
  autoFocus?: boolean;
}) {
  const [show, setShow] = useState(false);
  return (
    <div className="relative">
      <Input
        id={id}
        type={show ? "text" : "password"}
        autoFocus={autoFocus}
        autoComplete={id === "currentPassword" ? "current-password" : "new-password"}
        {...register(id)}
      />
      <button
        type="button"
        aria-label={show ? "Hide password" : "Show password"}
        onClick={() => setShow((v) => !v)}
        className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-muted-foreground hover:text-foreground"
        tabIndex={-1}
      >
        {show ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
      </button>
    </div>
  );
}

// ChangePasswordCard: form component for the Profile tab. Submits to the
// authenticated /api/v1/users/current/password endpoint; the server revokes
// other refresh tokens on success but leaves THIS session's access token
// alone, so we do not force the caller to re-login.
export function ChangePasswordCard() {
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>();
  const [serverError, setServerError] = useState("");

  const mutate = useMutation({
    mutationFn: (values: { currentPassword: string; newPassword: string }) =>
      api.changePassword(values),
    onSuccess: () => {
      toast.success("Password changed");
      reset();
      setServerError("");
    },
    onError: (e) => {
      const msg = e instanceof ApiError ? e.message : "Unknown error";
      setServerError(msg);
    },
  });

  async function onSubmit(values: FormValues) {
    const parsed = schema.safeParse(values);
    if (!parsed.success) {
      setServerError(parsed.error.issues[0]?.message ?? "Invalid input");
      return;
    }
    setServerError("");
    mutate.mutate({
      currentPassword: parsed.data.currentPassword,
      newPassword: parsed.data.newPassword,
    });
  }

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader className="p-4 pb-0">
          <h2 className="text-lg font-semibold">Change password</h2>
          <p className="text-sm text-muted-foreground">
            Update your login password. Other browsers signed in as you will be
            signed out on their next refresh.
          </p>
        </CardHeader>
        <CardContent className="p-4">
          <form
            onSubmit={handleSubmit(onSubmit)}
            className="max-w-sm space-y-4"
            aria-label="Change password form"
          >
            <div className="space-y-1.5">
              <Label htmlFor="currentPassword">Current password</Label>
              <PasswordField
                id="currentPassword"
                register={register}
                autoFocus
              />
              {errors.currentPassword && (
                <p className="text-xs text-destructive">
                  {errors.currentPassword.message}
                </p>
              )}
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="newPassword">New password</Label>
              <PasswordField id="newPassword" register={register} />
              {errors.newPassword && (
                <p className="text-xs text-destructive">
                  {errors.newPassword.message}
                </p>
              )}
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="confirmNewPassword">Confirm new password</Label>
              <PasswordField id="confirmNewPassword" register={register} />
              {errors.confirmNewPassword && (
                <p className="text-xs text-destructive">
                  {errors.confirmNewPassword.message}
                </p>
              )}
            </div>
            <Button
              type="submit"
              disabled={isSubmitting || mutate.isPending}
              data-testid="change-password-submit"
            >
              {mutate.isPending ? "Changing..." : "Change password"}
            </Button>
            {serverError && (
              <p className="text-sm text-destructive" role="alert">
                {serverError}
              </p>
            )}
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
