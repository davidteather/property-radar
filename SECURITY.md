# Security Policy

## Reporting a vulnerability

Please report security vulnerabilities through **GitHub's private vulnerability
reporting**: go to the repository's **Security** tab → **Report a vulnerability**
(Security Advisories). This keeps the report private until a fix is available.

If you can't use that form, email
[contact.davidteather@gmail.com](mailto:contact.davidteather@gmail.com).

**Please do not open a public issue for a vulnerability.**

## Scope

This policy covers the code in this repository. Property Radar is a
single-operator, self-hosted tool: **secrets (bearer token, S3 keys, proxy keys)
live only in the operator's environment and are never committed.** Each deployer
runs their own instance and is responsible for that instance's exposure:
network access, TLS, and how the public `/img` proxy is gated. See the README's
[Responsible use](README.md#responsible-use) section.

## Supported versions

The latest `main` is supported. Fixes land there; there are no maintained
backport branches.
