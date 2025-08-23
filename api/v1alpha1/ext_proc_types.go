package v1alpha1

// ExtProcPolicy defines the configuration for the Envoy External Processing filter.
//
// +kubebuilder:validation:ExactlyOneOf=extensionRef;disable
type ExtProcPolicy struct {
	// ExtensionRef references the GatewayExtension that should be used for external processing.
	// +optional
	ExtensionRef *NamespacedObjectReference `json:"extensionRef,omitempty"`

	// ProcessingMode defines how the filter should interact with the request/response streams
	// +optional
	ProcessingMode *ProcessingMode `json:"processingMode,omitempty"`

	// Disable all external processing filters.
	// Can be used to disable external processing policies applied at a higher level in the config hierarchy.
	// +optional
	Disable *PolicyDisable `json:"disable,omitempty"`
}

// ProcessingMode defines how the filter should interact with the request/response streams
type ProcessingMode struct {
	// RequestHeaderMode determines how to handle the request headers
	// +kubebuilder:validation:Enum=DEFAULT;SEND;SKIP
	// +kubebuilder:default=SEND
	// +optional
	RequestHeaderMode *string `json:"requestHeaderMode,omitempty"`

	// ResponseHeaderMode determines how to handle the response headers
	// +kubebuilder:validation:Enum=DEFAULT;SEND;SKIP
	// +kubebuilder:default=SEND
	// +optional
	ResponseHeaderMode *string `json:"responseHeaderMode,omitempty"`

	// RequestBodyMode determines how to handle the request body
	// +kubebuilder:validation:Enum=NONE;STREAMED;BUFFERED;BUFFERED_PARTIAL;FULL_DUPLEX_STREAMED
	// +kubebuilder:default=NONE
	// +optional
	RequestBodyMode *string `json:"requestBodyMode,omitempty"`

	// ResponseBodyMode determines how to handle the response body
	// +kubebuilder:validation:Enum=NONE;STREAMED;BUFFERED;BUFFERED_PARTIAL;FULL_DUPLEX_STREAMED
	// +kubebuilder:default=NONE
	// +optional
	ResponseBodyMode *string `json:"responseBodyMode,omitempty"`

	// RequestTrailerMode determines how to handle the request trailers
	// +kubebuilder:validation:Enum=DEFAULT;SEND;SKIP
	// +kubebuilder:default=SKIP
	// +optional
	RequestTrailerMode *string `json:"requestTrailerMode,omitempty"`

	// ResponseTrailerMode determines how to handle the response trailers
	// +kubebuilder:validation:Enum=DEFAULT;SEND;SKIP
	// +kubebuilder:default=SKIP
	// +optional
	ResponseTrailerMode *string `json:"responseTrailerMode,omitempty"`
}
