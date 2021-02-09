/*
Copyright 2020 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package bucket

import (
	"context"
	"fmt"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"reflect"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	"github.com/crossplane/provider-aws/apis/s3/v1beta1"
	awsclient "github.com/crossplane/provider-aws/pkg/clients"
	"github.com/crossplane/provider-aws/pkg/clients/s3"
)

const (
	sseGetFailed    = "cannot get Bucket encryption configuration"
	ssePutFailed    = "cannot put Bucket encryption configuration"
	sseDeleteFailed = "cannot delete Bucket encryption configuration"
)

// SSEConfigurationClient is the client for API methods and reconciling the ServerSideEncryptionConfiguration
type SSEConfigurationClient struct {
	client s3.BucketClient
	logger logging.Logger
}

// LateInitialize does nothing because the resource might have been deleted by
// the user.
func (in *SSEConfigurationClient) LateInitialize(ctx context.Context, bucket *v1beta1.Bucket) error {
	external, err := in.client.GetBucketEncryptionRequest(&awss3.GetBucketEncryptionInput{Bucket: awsclient.String(meta.GetExternalName(bucket))}).Send(ctx)
	if err != nil {
		// Short stop method for requests without a server side encryption
		if s3.SSEConfigurationNotFound(err) {
			return nil
		}
		return awsclient.Wrap(err, sseGetFailed)
	}

	// We need the second check here because by default the SSE is not set
	if external.GetBucketEncryptionOutput == nil || external.ServerSideEncryptionConfiguration == nil {
		return nil
	}

	in.logger.Debug(fmt.Sprintf("called LateInitialize for %s", reflect.TypeOf(in).Elem().Name()))

	if bucket.Spec.ForProvider.ServerSideEncryptionConfiguration == nil {
		bucket.Spec.ForProvider.ServerSideEncryptionConfiguration = &v1beta1.ServerSideEncryptionConfiguration{}
	}

	bucket.Spec.ForProvider.ServerSideEncryptionConfiguration.Rules = GenerateLocalBucketEncryption(external.ServerSideEncryptionConfiguration)

	return nil
}

// NewSSEConfigurationClient creates the client for Server Side Encryption Configuration
func NewSSEConfigurationClient(client s3.BucketClient, l logging.Logger) *SSEConfigurationClient {
	return &SSEConfigurationClient{client: client, logger: l}
}

// Observe checks if the resource exists and if it matches the local configuration
func (in *SSEConfigurationClient) Observe(ctx context.Context, bucket *v1beta1.Bucket) (ResourceStatus, error) { // nolint:gocyclo
	config := bucket.Spec.ForProvider.ServerSideEncryptionConfiguration
	external, err := in.client.GetBucketEncryptionRequest(&awss3.GetBucketEncryptionInput{Bucket: awsclient.String(meta.GetExternalName(bucket))}).Send(ctx)
	if err != nil {
		if s3.SSEConfigurationNotFound(err) && config == nil {
			return Updated, nil
		}
		return NeedsUpdate, awsclient.Wrap(resource.Ignore(s3.SSEConfigurationNotFound, err), sseGetFailed)
	}

	switch {
	case external.ServerSideEncryptionConfiguration != nil && config == nil:
		return NeedsDeletion, nil
	case external.ServerSideEncryptionConfiguration == nil && config == nil:
		return Updated, nil
	case external.ServerSideEncryptionConfiguration == nil && config != nil:
		return NeedsUpdate, nil
	case len(external.ServerSideEncryptionConfiguration.Rules) != len(config.Rules):
		return NeedsUpdate, nil
	}

	for i, Rule := range config.Rules {
		outputRule := external.ServerSideEncryptionConfiguration.Rules[i].ApplyServerSideEncryptionByDefault
		if awsclient.StringValue(outputRule.KMSMasterKeyID) != awsclient.StringValue(Rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID) {
			return NeedsUpdate, nil
		}
		if string(outputRule.SSEAlgorithm) != Rule.ApplyServerSideEncryptionByDefault.SSEAlgorithm {
			return NeedsUpdate, nil
		}
	}

	return Updated, nil
}

// GeneratePutBucketEncryptionInput creates the input for the PutBucketEncryption request for the S3 Client
func GeneratePutBucketEncryptionInput(name string, config *v1beta1.ServerSideEncryptionConfiguration) *awss3.PutBucketEncryptionInput {
	bei := &awss3.PutBucketEncryptionInput{
		Bucket:                            awsclient.String(name),
		ServerSideEncryptionConfiguration: &awss3.ServerSideEncryptionConfiguration{
			Rules: make([]awss3.ServerSideEncryptionRule, len(config.Rules)),
		},
	}
	for i, rule := range config.Rules {
		bei.ServerSideEncryptionConfiguration.Rules[i] = awss3.ServerSideEncryptionRule{
			ApplyServerSideEncryptionByDefault: &awss3.ServerSideEncryptionByDefault{
				KMSMasterKeyID: rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID,
				SSEAlgorithm:   awss3.ServerSideEncryption(rule.ApplyServerSideEncryptionByDefault.SSEAlgorithm),
			},
		}
	}
	return bei
}


// GenerateLocalBucketEncryption creates the local ServerSideEncryptionConfiguration from the S3 Client request
func GenerateLocalBucketEncryption(config *awss3.ServerSideEncryptionConfiguration) []v1beta1.ServerSideEncryptionRule {
	rules := make([]v1beta1.ServerSideEncryptionRule, len(config.Rules))
	for i, rule := range config.Rules {
		rules[i] = v1beta1.ServerSideEncryptionRule{
			ApplyServerSideEncryptionByDefault: v1beta1.ServerSideEncryptionByDefault{
				KMSMasterKeyID: rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID,
				SSEAlgorithm:   string(rule.ApplyServerSideEncryptionByDefault.SSEAlgorithm),
			},
		}
	}
	return rules
}

// CreateOrUpdate sends a request to have resource created on awsclient.
func (in *SSEConfigurationClient) CreateOrUpdate(ctx context.Context, bucket *v1beta1.Bucket) error {
	if bucket.Spec.ForProvider.ServerSideEncryptionConfiguration == nil {
		return nil
	}
	input := GeneratePutBucketEncryptionInput(meta.GetExternalName(bucket), bucket.Spec.ForProvider.ServerSideEncryptionConfiguration)
	_, err := in.client.PutBucketEncryptionRequest(input).Send(ctx)
	return awsclient.Wrap(err, ssePutFailed)
}

// Delete creates the request to delete the resource on AWS or set it to the default value.
func (in *SSEConfigurationClient) Delete(ctx context.Context, bucket *v1beta1.Bucket) error {
	_, err := in.client.DeleteBucketEncryptionRequest(
		&awss3.DeleteBucketEncryptionInput{
			Bucket: awsclient.String(meta.GetExternalName(bucket)),
		},
	).Send(ctx)
	return awsclient.Wrap(err, sseDeleteFailed)
}
