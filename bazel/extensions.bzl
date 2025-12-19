"""kube-parcel module extension for Bazel.

This extension allows external projects to configure kube-parcel settings
at the module level.

Usage in MODULE.bazel:
    bazel_dep(name = "kube_parcel", version = "0.0.1")
    
    kube_parcel = use_extension("@kube_parcel//bazel:extensions.bzl", "kube_parcel")
    kube_parcel.options(
        default_exec_mode = "docker",
    )

Then in BUILD files:
    load("@kube_parcel//bazel:defs.bzl", "kube_parcel_test")
    
    kube_parcel_test(
        name = "my_test",
        charts = [":my_chart"],
    )
"""

def _kube_parcel_impl(module_ctx):
    """Implementation of the kube_parcel module extension.

    This extension collects options from all modules using kube_parcel
    and can be extended to provide additional repository rules or
    configuration.
    """

    # Collect options from all modules
    for mod in module_ctx.modules:
        for options in mod.tags.options:
            # Currently we just validate options
            # Future: could generate a config file or additional targets
            if options.default_exec_mode not in ["docker", "k8s", ""]:
                fail("Invalid default_exec_mode: {} (must be 'docker' or 'k8s')".format(
                    options.default_exec_mode,
                ))

    # No repositories to generate currently
    # The extension primarily validates configuration and could be
    # extended to:
    # - Download specific runner images
    # - Configure default settings
    # - Set up remote registries

_options = tag_class(
    attrs = {
        "default_exec_mode": attr.string(
            default = "docker",
            doc = "Default execution mode for kube_parcel_test rules. Can be overridden per-target or via --config flag.",
        ),
        "runner_image": attr.string(
            default = "",
            doc = "Custom runner image to use instead of the default. Leave empty to use built-in runner.",
        ),
    },
    doc = "Configure kube-parcel options for the module.",
)

kube_parcel = module_extension(
    implementation = _kube_parcel_impl,
    tag_classes = {
        "options": _options,
    },
    doc = """Module extension for configuring kube-parcel.

This extension allows modules to configure default settings for
kube-parcel test rules. Settings can be overridden at the target
level or via bazelrc configs.

Example:
    kube_parcel = use_extension("@kube_parcel//bazel:extensions.bzl", "kube_parcel")
    kube_parcel.options(default_exec_mode = "k8s")
""",
)
