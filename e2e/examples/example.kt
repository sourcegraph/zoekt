package com.airbnb.epoxy.preload
//      ^^^ reference com/
//          ^^^^^^ reference com/airbnb/
//                 ^^^^^ reference com/airbnb/epoxy/
//                       ^^^^^^^ reference com/airbnb/epoxy/preload/

import android.content.Context
import android.view.View
import android.widget.ImageView
import androidx.annotation.IdRes
//     ^^^^^^^^ reference androidx/
//              ^^^^^^^^^^ reference androidx/annotation/
//                         ^^^^^ reference androidx/annotation/IdRes#
import androidx.annotation.Px
//     ^^^^^^^^ reference androidx/
//              ^^^^^^^^^^ reference androidx/annotation/
//                         ^^ reference androidx/annotation/Px#
import androidx.recyclerview.widget.LinearLayoutManager
//     ^^^^^^^^ reference androidx/
import androidx.recyclerview.widget.RecyclerView
//     ^^^^^^^^ reference androidx/
import com.airbnb.epoxy.BaseEpoxyAdapter
//     ^^^ reference com/
//         ^^^^^^ reference com/airbnb/
//                ^^^^^ reference com/airbnb/epoxy/
import com.airbnb.epoxy.EpoxyAdapter
//     ^^^ reference com/
//         ^^^^^^ reference com/airbnb/
//                ^^^^^ reference com/airbnb/epoxy/
import com.airbnb.epoxy.EpoxyController
//     ^^^ reference com/
//         ^^^^^^ reference com/airbnb/
//                ^^^^^ reference com/airbnb/epoxy/
import com.airbnb.epoxy.EpoxyModel
//     ^^^ reference com/
//         ^^^^^^ reference com/airbnb/
//                ^^^^^ reference com/airbnb/epoxy/
import com.airbnb.epoxy.getModelForPositionInternal
//     ^^^ reference com/
//         ^^^^^^ reference com/airbnb/
//                ^^^^^ reference com/airbnb/epoxy/
//                      ^^^^^^^^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/InternalExposerKt#getModelForPositionInternal().
import kotlin.math.max
//     ^^^^^^ reference kotlin/
//            ^^^^ reference kotlin/math/
import kotlin.math.min
//     ^^^^^^ reference kotlin/
//            ^^^^ reference kotlin/math/

/**
 * A scroll listener that prefetches view content.
 *
 * To use this, create implementations of [EpoxyModelPreloader] for each EpoxyModel class that you want to preload.
 * Then, use the [EpoxyPreloader.with] methods to create an instance that preloads models of that type.
 * Finally, add the resulting scroll listener to your RecyclerView.
 *
 * If you are using [com.airbnb.epoxy.EpoxyRecyclerView] then use [com.airbnb.epoxy.EpoxyRecyclerView.addPreloader]
 * to setup the preloader as a listener.
 *
 * Otherwise there is a [RecyclerView.addEpoxyPreloader] extension for easy usage.
 */
class EpoxyPreloader<P : PreloadRequestHolder> private constructor(
//    ^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#  public final class EpoxyPreloader<P : com.airbnb.epoxy.preload.PreloadRequestHolder>
//                   ^ definition com/airbnb/epoxy/preload/EpoxyPreloader#[P]  <P : com.airbnb.epoxy.preload.PreloadRequestHolder>
//                       ^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/PreloadRequestHolder#
//                                                     ^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`().  private constructor EpoxyPreloader<P : com.airbnb.epoxy.preload.PreloadRequestHolder>(adapter: [ERROR : BaseEpoxyAdapter], preloadTargetFactory: () -> P, errorHandler: com.airbnb.epoxy.preload.PreloadErrorHandler /* = ([ERROR : Context], kotlin.RuntimeException /* = java.lang.RuntimeException */) -> kotlin.Unit */, maxItemsToPreload: kotlin.Int, modelPreloaders: kotlin.collections.List<com.airbnb.epoxy.preload.EpoxyModelPreloader<*, *, out P>>)
    private val adapter: BaseEpoxyAdapter,
//              ^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#adapter.  private final val adapter: [ERROR : BaseEpoxyAdapter]
//              ^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#getAdapter().  private final val adapter: [ERROR : BaseEpoxyAdapter]
//              ^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`().(adapter)  value-parameter adapter: [ERROR : BaseEpoxyAdapter]
    preloadTargetFactory: () -> P,
//  ^^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`().(preloadTargetFactory)  value-parameter preloadTargetFactory: () -> P
//                              ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#[P]
    errorHandler: PreloadErrorHandler,
//  ^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`().(errorHandler)  value-parameter errorHandler: com.airbnb.epoxy.preload.PreloadErrorHandler /* = ([ERROR : Context], kotlin.RuntimeException /* = java.lang.RuntimeException */) -> kotlin.Unit */
//                ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/PreloadErrorHandler#
    private val maxItemsToPreload: Int,
//              ^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#maxItemsToPreload.  private final val maxItemsToPreload: kotlin.Int
//              ^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#getMaxItemsToPreload().  private final val maxItemsToPreload: kotlin.Int
//              ^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`().(maxItemsToPreload)  value-parameter maxItemsToPreload: kotlin.Int
//                                 ^^^ reference kotlin/Int#
    modelPreloaders: List<EpoxyModelPreloader<*, *, out P>>
//  ^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`().(modelPreloaders)  value-parameter modelPreloaders: kotlin.collections.List<com.airbnb.epoxy.preload.EpoxyModelPreloader<*, *, out P>>
//                   ^^^^ reference kotlin/collections/List#
//                        ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyModelPreloader#
//                                                      ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#[P]
) : RecyclerView.OnScrollListener() {

    private var lastVisibleRange: IntRange = IntRange.EMPTY
//              ^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#lastVisibleRange.  private final var lastVisibleRange: kotlin.ranges.IntRange
//              ^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#getLastVisibleRange().  private final var lastVisibleRange: kotlin.ranges.IntRange
//              ^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#setLastVisibleRange().  private final var lastVisibleRange: kotlin.ranges.IntRange
//                                ^^^^^^^^ reference kotlin/ranges/IntRange#
//                                           ^^^^^^^^ reference kotlin/ranges/IntRange#Companion#
//                                                    ^^^^^ reference kotlin/ranges/IntRange#Companion#EMPTY.
//                                                    ^^^^^ reference kotlin/ranges/IntRange#Companion#getEMPTY().
    private var lastPreloadRange: IntProgression = IntRange.EMPTY
//              ^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#lastPreloadRange.  private final var lastPreloadRange: kotlin.ranges.IntProgression
//              ^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#getLastPreloadRange().  private final var lastPreloadRange: kotlin.ranges.IntProgression
//              ^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#setLastPreloadRange().  private final var lastPreloadRange: kotlin.ranges.IntProgression
//                                ^^^^^^^^^^^^^^ reference kotlin/ranges/IntProgression#
//                                                 ^^^^^^^^ reference kotlin/ranges/IntRange#Companion#
//                                                          ^^^^^ reference kotlin/ranges/IntRange#Companion#EMPTY.
//                                                          ^^^^^ reference kotlin/ranges/IntRange#Companion#getEMPTY().
    private var totalItemCount = -1
//              ^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#totalItemCount.  private final var totalItemCount: kotlin.Int
//              ^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#getTotalItemCount().  private final var totalItemCount: kotlin.Int
//              ^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#setTotalItemCount().  private final var totalItemCount: kotlin.Int
//                               ^ reference kotlin/Int#unaryMinus().
    private var scrollState: Int = RecyclerView.SCROLL_STATE_IDLE
//              ^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#scrollState.  private final var scrollState: kotlin.Int
//              ^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#getScrollState().  private final var scrollState: kotlin.Int
//              ^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#setScrollState().  private final var scrollState: kotlin.Int
//                           ^^^ reference kotlin/Int#

    private val modelPreloaders: Map<Class<out EpoxyModel<*>>, EpoxyModelPreloader<*, *, out P>> =
//              ^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#modelPreloaders.  private final val modelPreloaders: kotlin.collections.Map<java.lang.Class<out [ERROR : EpoxyModel<*>]<out [ERROR : *]>>, com.airbnb.epoxy.preload.EpoxyModelPreloader<*, *, out P>>
//              ^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#getModelPreloaders().  private final val modelPreloaders: kotlin.collections.Map<java.lang.Class<out [ERROR : EpoxyModel<*>]<out [ERROR : *]>>, com.airbnb.epoxy.preload.EpoxyModelPreloader<*, *, out P>>
//                               ^^^ reference kotlin/collections/Map#
//                                   ^^^^^ reference java/lang/Class#
//                                                             ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyModelPreloader#
//                                                                                           ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#[P]
        modelPreloaders.associateBy { it.modelType }
//      ^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`().(modelPreloaders)
//                      ^^^^^^^^^^^ reference kotlin/collections/CollectionsKt#associateBy(+18).
//                                    ^^ reference local0
//                                       ^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyModelPreloader#modelType.
//                                       ^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyModelPreloader#getModelType().

    private val requestHolderFactory =
//              ^^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#requestHolderFactory.  private final val requestHolderFactory: com.airbnb.epoxy.preload.PreloadTargetProvider<P>
//              ^^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#getRequestHolderFactory().  private final val requestHolderFactory: com.airbnb.epoxy.preload.PreloadTargetProvider<P>
        PreloadTargetProvider(maxItemsToPreload, preloadTargetFactory)
//      ^^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/PreloadTargetProvider#`<init>`().
//                            ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#maxItemsToPreload.
//                            ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getMaxItemsToPreload().
//                                               ^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`().(preloadTargetFactory)

    private val viewDataCache = PreloadableViewDataProvider(adapter, errorHandler)
//              ^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#viewDataCache.  private final val viewDataCache: com.airbnb.epoxy.preload.PreloadableViewDataProvider
//              ^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#getViewDataCache().  private final val viewDataCache: com.airbnb.epoxy.preload.PreloadableViewDataProvider
//                              ^^^^^^^^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/PreloadableViewDataProvider#`<init>`().
//                                                          ^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#adapter.
//                                                          ^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getAdapter().
//                                                                   ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`().(errorHandler)

    constructor(
//  ^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+1).  public constructor EpoxyPreloader<P : com.airbnb.epoxy.preload.PreloadRequestHolder>(epoxyController: [ERROR : EpoxyController], requestHolderFactory: () -> P, errorHandler: com.airbnb.epoxy.preload.PreloadErrorHandler /* = ([ERROR : Context], kotlin.RuntimeException /* = java.lang.RuntimeException */) -> kotlin.Unit */, maxItemsToPreload: kotlin.Int, modelPreloaders: kotlin.collections.List<com.airbnb.epoxy.preload.EpoxyModelPreloader<*, *, out P>>)
        epoxyController: EpoxyController,
//      ^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+1).(epoxyController)  value-parameter epoxyController: [ERROR : EpoxyController]
        requestHolderFactory: () -> P,
//      ^^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+1).(requestHolderFactory)  value-parameter requestHolderFactory: () -> P
//                                  ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#[P]
        errorHandler: PreloadErrorHandler,
//      ^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+1).(errorHandler)  value-parameter errorHandler: com.airbnb.epoxy.preload.PreloadErrorHandler /* = ([ERROR : Context], kotlin.RuntimeException /* = java.lang.RuntimeException */) -> kotlin.Unit */
//                    ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/PreloadErrorHandler#
        maxItemsToPreload: Int,
//      ^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+1).(maxItemsToPreload)  value-parameter maxItemsToPreload: kotlin.Int
//                         ^^^ reference kotlin/Int#
        modelPreloaders: List<EpoxyModelPreloader<*, *, out P>>
//      ^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+1).(modelPreloaders)  value-parameter modelPreloaders: kotlin.collections.List<com.airbnb.epoxy.preload.EpoxyModelPreloader<*, *, out P>>
//                       ^^^^ reference kotlin/collections/List#
//                            ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyModelPreloader#
//                                                          ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#[P]
    ) : this(
        epoxyController.adapter,
//      ^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+1).(epoxyController)
        requestHolderFactory,
//      ^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+1).(requestHolderFactory)
        errorHandler,
//      ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+1).(errorHandler)
        maxItemsToPreload,
//      ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+1).(maxItemsToPreload)
        modelPreloaders
//      ^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+1).(modelPreloaders)
    )

    constructor(
//  ^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+2).  public constructor EpoxyPreloader<P : com.airbnb.epoxy.preload.PreloadRequestHolder>(adapter: [ERROR : EpoxyAdapter], requestHolderFactory: () -> P, errorHandler: com.airbnb.epoxy.preload.PreloadErrorHandler /* = ([ERROR : Context], kotlin.RuntimeException /* = java.lang.RuntimeException */) -> kotlin.Unit */, maxItemsToPreload: kotlin.Int, modelPreloaders: kotlin.collections.List<com.airbnb.epoxy.preload.EpoxyModelPreloader<*, *, out P>>)
        adapter: EpoxyAdapter,
//      ^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+2).(adapter)  value-parameter adapter: [ERROR : EpoxyAdapter]
        requestHolderFactory: () -> P,
//      ^^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+2).(requestHolderFactory)  value-parameter requestHolderFactory: () -> P
//                                  ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#[P]
        errorHandler: PreloadErrorHandler,
//      ^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+2).(errorHandler)  value-parameter errorHandler: com.airbnb.epoxy.preload.PreloadErrorHandler /* = ([ERROR : Context], kotlin.RuntimeException /* = java.lang.RuntimeException */) -> kotlin.Unit */
//                    ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/PreloadErrorHandler#
        maxItemsToPreload: Int,
//      ^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+2).(maxItemsToPreload)  value-parameter maxItemsToPreload: kotlin.Int
//                         ^^^ reference kotlin/Int#
        modelPreloaders: List<EpoxyModelPreloader<*, *, out P>>
//      ^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+2).(modelPreloaders)  value-parameter modelPreloaders: kotlin.collections.List<com.airbnb.epoxy.preload.EpoxyModelPreloader<*, *, out P>>
//                       ^^^^ reference kotlin/collections/List#
//                            ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyModelPreloader#
//                                                          ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#[P]
    ) : this(
        adapter as BaseEpoxyAdapter,
//      ^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+2).(adapter)
        requestHolderFactory,
//      ^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+2).(requestHolderFactory)
        errorHandler,
//      ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+2).(errorHandler)
        maxItemsToPreload,
//      ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+2).(maxItemsToPreload)
        modelPreloaders
//      ^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#`<init>`(+2).(modelPreloaders)
    )

    init {
        require(maxItemsToPreload > 0) {
//      ^^^^^^^ reference kotlin/PreconditionsKt#require(+1).
//              ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#maxItemsToPreload.
//              ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getMaxItemsToPreload().
//                                ^ reference kotlin/Int#compareTo(+3).
            "maxItemsToPreload must be greater than 0. Was $maxItemsToPreload"
//                                                          ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#maxItemsToPreload.
//                                                          ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getMaxItemsToPreload().
        }
    }

    override fun onScrollStateChanged(recyclerView: RecyclerView, newState: Int) {
//               ^^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#onScrollStateChanged().  public open fun onScrollStateChanged(recyclerView: [ERROR : RecyclerView], newState: kotlin.Int)
//                                    ^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#onScrollStateChanged().(recyclerView)  value-parameter recyclerView: [ERROR : RecyclerView]
//                                                                ^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#onScrollStateChanged().(newState)  value-parameter newState: kotlin.Int
//                                                                          ^^^ reference kotlin/Int#
        scrollState = newState
//      ^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#scrollState.
//      ^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getScrollState().
//      ^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#setScrollState().
//                    ^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#onScrollStateChanged().(newState)
    }

    override fun onScrolled(recyclerView: RecyclerView, dx: Int, dy: Int) {
//               ^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#onScrolled().  public open fun onScrolled(recyclerView: [ERROR : RecyclerView], dx: kotlin.Int, dy: kotlin.Int)
//                          ^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#onScrolled().(recyclerView)  value-parameter recyclerView: [ERROR : RecyclerView]
//                                                      ^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#onScrolled().(dx)  value-parameter dx: kotlin.Int
//                                                          ^^^ reference kotlin/Int#
//                                                               ^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#onScrolled().(dy)  value-parameter dy: kotlin.Int
//                                                                   ^^^ reference kotlin/Int#
        if (dx == 0 && dy == 0) {
//          ^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#onScrolled().(dx)
//             ^^ reference kotlin/Int#equals().
//                     ^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#onScrolled().(dy)
//                        ^^ reference kotlin/Int#equals().
            // Sometimes flings register a bunch of 0 dx/dy scroll events. To avoid redundant prefetching we just skip these
            // Additionally, the first RecyclerView layout notifies a scroll of 0, since that can be an important time for
            // performance (eg page load) we avoid prefetching at the same time.
            return
        }

        if (dx.isFling() || dy.isFling()) {
//          ^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#onScrolled().(dx)
//             ^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#isFling().
//                          ^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#onScrolled().(dy)
//                             ^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#isFling().
            // We avoid preloading during flings for two reasons
            // 1. Image requests are expensive and we don't want to drop frames on fling
            // 2. We'll likely scroll past the preloading item anyway
            return
        }

        // Update item count before anything else because validations depend on it
        totalItemCount = recyclerView.adapter?.itemCount ?: 0
//      ^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#totalItemCount.
//      ^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getTotalItemCount().
//      ^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#setTotalItemCount().
//                       ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#onScrolled().(recyclerView)

        val layoutManager = recyclerView.layoutManager as LinearLayoutManager
//          ^^^^^^^^^^^^^ definition local1  val layoutManager: [ERROR : LinearLayoutManager]
//                          ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#onScrolled().(recyclerView)
        val firstVisiblePosition = layoutManager.findFirstVisibleItemPosition()
//          ^^^^^^^^^^^^^^^^^^^^ definition local2  val firstVisiblePosition: [ERROR : <ERROR FUNCTION RETURN TYPE>]
//                                 ^^^^^^^^^^^^^ reference local1
        val lastVisiblePosition = layoutManager.findLastVisibleItemPosition()
//          ^^^^^^^^^^^^^^^^^^^ definition local3  val lastVisiblePosition: [ERROR : <ERROR FUNCTION RETURN TYPE>]
//                                ^^^^^^^^^^^^^ reference local1

        if (firstVisiblePosition.isInvalid() || lastVisiblePosition.isInvalid()) {
//          ^^^^^^^^^^^^^^^^^^^^ reference local2
//                                              ^^^^^^^^^^^^^^^^^^^ reference local3
            lastVisibleRange = IntRange.EMPTY
//          ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#lastVisibleRange.
//          ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getLastVisibleRange().
//          ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#setLastVisibleRange().
//                             ^^^^^^^^ reference kotlin/ranges/IntRange#Companion#
//                                      ^^^^^ reference kotlin/ranges/IntRange#Companion#EMPTY.
//                                      ^^^^^ reference kotlin/ranges/IntRange#Companion#getEMPTY().
            lastPreloadRange = IntRange.EMPTY
//          ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#lastPreloadRange.
//          ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getLastPreloadRange().
//          ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#setLastPreloadRange().
//                             ^^^^^^^^ reference kotlin/ranges/IntRange#Companion#
//                                      ^^^^^ reference kotlin/ranges/IntRange#Companion#EMPTY.
//                                      ^^^^^ reference kotlin/ranges/IntRange#Companion#getEMPTY().
            return
        }

        val visibleRange = IntRange(firstVisiblePosition, lastVisiblePosition)
//          ^^^^^^^^^^^^ definition local4  val visibleRange: kotlin.ranges.IntRange
//                         ^^^^^^^^ reference kotlin/ranges/IntRange#`<init>`().
//                                  ^^^^^^^^^^^^^^^^^^^^ reference local2
//                                                        ^^^^^^^^^^^^^^^^^^^ reference local3
        if (visibleRange == lastVisibleRange) {
//          ^^^^^^^^^^^^ reference local4
//                       ^^ reference kotlin/ranges/IntRange#equals().
//                          ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#lastVisibleRange.
//                          ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getLastVisibleRange().
//                          ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#setLastVisibleRange().
            return
        }

        val isIncreasing =
//          ^^^^^^^^^^^^ definition local5  val isIncreasing: kotlin.Boolean
            visibleRange.first > lastVisibleRange.first || visibleRange.last > lastVisibleRange.last
//          ^^^^^^^^^^^^ reference local4
//                       ^^^^^ reference kotlin/ranges/IntRange#first.
//                       ^^^^^ reference kotlin/ranges/IntRange#getFirst().
//                             ^ reference kotlin/Int#compareTo(+3).
//                               ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#lastVisibleRange.
//                               ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getLastVisibleRange().
//                               ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#setLastVisibleRange().
//                                                ^^^^^ reference kotlin/ranges/IntRange#first.
//                                                ^^^^^ reference kotlin/ranges/IntRange#getFirst().
//                                                         ^^^^^^^^^^^^ reference local4
//                                                                      ^^^^ reference kotlin/ranges/IntRange#last.
//                                                                      ^^^^ reference kotlin/ranges/IntRange#getLast().
//                                                                           ^ reference kotlin/Int#compareTo(+3).
//                                                                             ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#lastVisibleRange.
//                                                                             ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getLastVisibleRange().
//                                                                             ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#setLastVisibleRange().
//                                                                                              ^^^^ reference kotlin/ranges/IntRange#last.
//                                                                                              ^^^^ reference kotlin/ranges/IntRange#getLast().

        val preloadRange =
//          ^^^^^^^^^^^^ definition local6  val preloadRange: kotlin.ranges.IntProgression
            calculatePreloadRange(firstVisiblePosition, lastVisiblePosition, isIncreasing)
//          ^^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#calculatePreloadRange().
//                                ^^^^^^^^^^^^^^^^^^^^ reference local2
//                                                      ^^^^^^^^^^^^^^^^^^^ reference local3
//                                                                           ^^^^^^^^^^^^ reference local5

        // Start preload for any items that weren't already preloaded
        preloadRange
//      ^^^^^^^^^^^^ reference local6
            .subtract(lastPreloadRange)
//           ^^^^^^^^ reference kotlin/collections/CollectionsKt#subtract(+9).
//                    ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#lastPreloadRange.
//                    ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getLastPreloadRange().
//                    ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#setLastPreloadRange().
            .forEach { preloadAdapterPosition(it) }
//           ^^^^^^^ reference kotlin/collections/CollectionsKt#forEach(+10).
//                     ^^^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#preloadAdapterPosition().
//                                            ^^ reference local7

        lastVisibleRange = visibleRange
//      ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#lastVisibleRange.
//      ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getLastVisibleRange().
//      ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#setLastVisibleRange().
//                         ^^^^^^^^^^^^ reference local4
        lastPreloadRange = preloadRange
//      ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#lastPreloadRange.
//      ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getLastPreloadRange().
//      ^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#setLastPreloadRange().
//                         ^^^^^^^^^^^^ reference local6
    }

    /**
     * @receiver The number of pixels scrolled.
     * @return True if this distance is large enough to be considered a fast fling.
     */
    private fun Int.isFling() = Math.abs(this) > FLING_THRESHOLD_PX
//              ^^^ reference kotlin/Int#
//                  ^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#isFling().  private final fun kotlin.Int.isFling(): kotlin.Boolean
//                              ^^^^ reference java/lang/Math#
//                                   ^^^ reference java/lang/Math#abs().
//                                       ^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#isFling().
//                                             ^ reference kotlin/Int#compareTo(+3).
//                                               ^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#FLING_THRESHOLD_PX.
//                                               ^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#getFLING_THRESHOLD_PX().

    private fun calculatePreloadRange(
//              ^^^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#calculatePreloadRange().  private final fun calculatePreloadRange(firstVisiblePosition: kotlin.Int, lastVisiblePosition: kotlin.Int, isIncreasing: kotlin.Boolean): kotlin.ranges.IntProgression
        firstVisiblePosition: Int,
//      ^^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#calculatePreloadRange().(firstVisiblePosition)  value-parameter firstVisiblePosition: kotlin.Int
//                            ^^^ reference kotlin/Int#
        lastVisiblePosition: Int,
//      ^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#calculatePreloadRange().(lastVisiblePosition)  value-parameter lastVisiblePosition: kotlin.Int
//                           ^^^ reference kotlin/Int#
        isIncreasing: Boolean
//      ^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#calculatePreloadRange().(isIncreasing)  value-parameter isIncreasing: kotlin.Boolean
//                    ^^^^^^^ reference kotlin/Boolean#
    ): IntProgression {
//     ^^^^^^^^^^^^^^ reference kotlin/ranges/IntProgression#
        val from = if (isIncreasing) lastVisiblePosition + 1 else firstVisiblePosition - 1
//          ^^^^ definition local8  val from: kotlin.Int
//                     ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#calculatePreloadRange().(isIncreasing)
//                                   ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#calculatePreloadRange().(lastVisiblePosition)
//                                                       ^ reference kotlin/Int#plus(+3).
//                                                                ^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#calculatePreloadRange().(firstVisiblePosition)
//                                                                                     ^ reference kotlin/Int#minus(+3).
        val to = from + if (isIncreasing) maxItemsToPreload - 1 else 1 - maxItemsToPreload
//          ^^ definition local9  val to: kotlin.Int
//               ^^^^ reference local8
//                    ^ reference kotlin/Int#plus(+3).
//                          ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#calculatePreloadRange().(isIncreasing)
//                                        ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#maxItemsToPreload.
//                                        ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getMaxItemsToPreload().
//                                                          ^ reference kotlin/Int#minus(+3).
//                                                                     ^ reference kotlin/Int#minus(+3).
//                                                                       ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#maxItemsToPreload.
//                                                                       ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getMaxItemsToPreload().

        return IntProgression.fromClosedRange(
//             ^^^^^^^^^^^^^^ reference kotlin/ranges/IntProgression#Companion#
//                            ^^^^^^^^^^^^^^^ reference kotlin/ranges/IntProgression#Companion#fromClosedRange().
            rangeStart = from.clampToAdapterRange(),
//          ^^^^^^^^^^ reference kotlin/ranges/IntProgression#Companion#fromClosedRange().(rangeStart)
//                       ^^^^ reference local8
//                            ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#clampToAdapterRange().
            rangeEnd = to.clampToAdapterRange(),
//          ^^^^^^^^ reference kotlin/ranges/IntProgression#Companion#fromClosedRange().(rangeEnd)
//                     ^^ reference local9
//                        ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#clampToAdapterRange().
            step = if (isIncreasing) 1 else -1
//          ^^^^ reference kotlin/ranges/IntProgression#Companion#fromClosedRange().(step)
//                     ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#calculatePreloadRange().(isIncreasing)
//                                          ^ reference kotlin/Int#unaryMinus().
        )
    }

    /** Check if an item index is valid. It may not be if the adapter is empty, or if adapter changes have been dispatched since the last layout pass. */
    private fun Int.isInvalid() = this == RecyclerView.NO_POSITION || this >= totalItemCount
//              ^^^ reference kotlin/Int#
//                  ^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#isInvalid().  private final fun kotlin.Int.isInvalid(): kotlin.Boolean
//                                ^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#isInvalid().
//                                     ^^ reference kotlin/Int#equals().
//                                                                    ^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#isInvalid().
//                                                                         ^^ reference kotlin/Int#compareTo(+3).
//                                                                            ^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#totalItemCount.
//                                                                            ^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getTotalItemCount().
//                                                                            ^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#setTotalItemCount().

    private fun Int.clampToAdapterRange() = min(totalItemCount - 1, max(this, 0))
//              ^^^ reference kotlin/Int#
//                  ^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#clampToAdapterRange().  private final fun kotlin.Int.clampToAdapterRange(): kotlin.Int
//                                          ^^^ reference kotlin/math/MathKt#min(+2).
//                                              ^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#totalItemCount.
//                                              ^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getTotalItemCount().
//                                              ^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#setTotalItemCount().
//                                                             ^ reference kotlin/Int#minus(+3).
//                                                                  ^^^ reference kotlin/math/MathKt#max(+2).
//                                                                      ^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#clampToAdapterRange().

    private fun preloadAdapterPosition(position: Int) {
//              ^^^^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#preloadAdapterPosition().  private final fun preloadAdapterPosition(position: kotlin.Int)
//                                     ^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#preloadAdapterPosition().(position)  value-parameter position: kotlin.Int
//                                               ^^^ reference kotlin/Int#
        @Suppress("UNCHECKED_CAST")
//       ^^^^^^^^ reference kotlin/Suppress#`<init>`().
        val epoxyModel = adapter.getModelForPositionInternal(position) as? EpoxyModel<Any>
//          ^^^^^^^^^^ definition local10  val epoxyModel: kotlin.Nothing
//                       ^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#adapter.
//                       ^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getAdapter().
//                                                           ^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#preloadAdapterPosition().(position)
//                                                                                    ^^^ reference kotlin/Any#
            ?: return

        @Suppress("UNCHECKED_CAST")
//       ^^^^^^^^ reference kotlin/Suppress#`<init>`().
        val preloader =
//          ^^^^^^^^^ definition local11  val preloader: com.airbnb.epoxy.preload.EpoxyModelPreloader<[ERROR : EpoxyModel<*>]<out [ERROR : *]>, com.airbnb.epoxy.preload.ViewMetadata?, P>
            modelPreloaders[epoxyModel::class.java] as? EpoxyModelPreloader<EpoxyModel<*>, ViewMetadata?, P>
//          ^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#modelPreloaders.
//          ^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getModelPreloaders().
//                          ^^^^^^^^^^ reference local10
//                                            ^^^^ reference kotlin/jvm/JvmClassMappingKt#java.
//                                                      ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyModelPreloader#
//                                                                                         ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/ViewMetadata#
//                                                                                                        ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#[P]
                ?: return

        viewDataCache
//      ^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#viewDataCache.
//      ^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getViewDataCache().
            .dataForModel(preloader, epoxyModel, position)
//           ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/PreloadableViewDataProvider#dataForModel().
//                        ^^^^^^^^^ reference local11
//                                   ^^^^^^^^^^ reference local10
//                                               ^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#preloadAdapterPosition().(position)
            .forEach { viewData ->
//           ^^^^^^^ reference kotlin/collections/CollectionsKt#forEach(+10).
//                     ^^^^^^^^ definition local12  value-parameter viewData: com.airbnb.epoxy.preload.ViewData<com.airbnb.epoxy.preload.ViewMetadata?>
                val preloadTarget = requestHolderFactory.next()
//                  ^^^^^^^^^^^^^ definition local13  val preloadTarget: P
//                                  ^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#requestHolderFactory.
//                                  ^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getRequestHolderFactory().
//                                                       ^^^^ reference com/airbnb/epoxy/preload/PreloadTargetProvider#next().
                preloader.startPreload(epoxyModel, preloadTarget, viewData)
//              ^^^^^^^^^ reference local11
//                        ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyModelPreloader#startPreload().
//                                     ^^^^^^^^^^ reference local10
//                                                 ^^^^^^^^^^^^^ reference local13
//                                                                ^^^^^^^^ reference local12
            }
    }

    /**
     * Cancels all current preload requests in progress.
     */
    fun cancelPreloadRequests() {
//      ^^^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#cancelPreloadRequests().  public final fun cancelPreloadRequests()
        requestHolderFactory.clearAll()
//      ^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#requestHolderFactory.
//      ^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#getRequestHolderFactory().
//                           ^^^^^^^^ reference com/airbnb/epoxy/preload/PreloadTargetProvider#clearAll().
    }

    companion object {
//            ^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#  public companion object

        /**
         *
         * Represents a threshold for fast scrolling.
         * This is a bit arbitrary and was determined by looking at values while flinging vs slow scrolling.
         * Ideally it would be based on DP, but this is simpler.
         */
        private const val FLING_THRESHOLD_PX = 75
//                        ^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#FLING_THRESHOLD_PX.  private const final val FLING_THRESHOLD_PX: kotlin.Int
//                        ^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#getFLING_THRESHOLD_PX().  private const final val FLING_THRESHOLD_PX: kotlin.Int

        /**
         * Helper to create a preload scroll listener. Add the result to your RecyclerView.
         * for different models or content types.
         *
         * @param maxItemsToPreload How many items to prefetch ahead of the last bound item
         * @param errorHandler Called when the preloader encounters an exception. By default this throws only
         * if the app is not in a debuggle model
         * @param modelPreloader Describes how view content for the EpoxyModel should be preloaded
         * @param requestHolderFactory Should create and return a new [PreloadRequestHolder] each time it is invoked
         */
        fun <P : PreloadRequestHolder> with(
//           ^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with().[P]  <P : com.airbnb.epoxy.preload.PreloadRequestHolder>
//               ^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/PreloadRequestHolder#
//                                     ^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with().  public final fun <P : com.airbnb.epoxy.preload.PreloadRequestHolder> with(epoxyController: [ERROR : EpoxyController], requestHolderFactory: () -> P, errorHandler: com.airbnb.epoxy.preload.PreloadErrorHandler /* = ([ERROR : Context], kotlin.RuntimeException /* = java.lang.RuntimeException */) -> kotlin.Unit */, maxItemsToPreload: kotlin.Int, modelPreloader: com.airbnb.epoxy.preload.EpoxyModelPreloader<out [ERROR : EpoxyModel<*>]<out [ERROR : *]>, out com.airbnb.epoxy.preload.ViewMetadata?, out P>): com.airbnb.epoxy.preload.EpoxyPreloader<P>
            epoxyController: EpoxyController,
//          ^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with().(epoxyController)  value-parameter epoxyController: [ERROR : EpoxyController]
            requestHolderFactory: () -> P,
//          ^^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with().(requestHolderFactory)  value-parameter requestHolderFactory: () -> P
//                                      ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with().[P]
            errorHandler: PreloadErrorHandler,
//          ^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with().(errorHandler)  value-parameter errorHandler: com.airbnb.epoxy.preload.PreloadErrorHandler /* = ([ERROR : Context], kotlin.RuntimeException /* = java.lang.RuntimeException */) -> kotlin.Unit */
//                        ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/PreloadErrorHandler#
            maxItemsToPreload: Int,
//          ^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with().(maxItemsToPreload)  value-parameter maxItemsToPreload: kotlin.Int
//                             ^^^ reference kotlin/Int#
            modelPreloader: EpoxyModelPreloader<out EpoxyModel<*>, out ViewMetadata?, out P>
//          ^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with().(modelPreloader)  value-parameter modelPreloader: com.airbnb.epoxy.preload.EpoxyModelPreloader<out [ERROR : EpoxyModel<*>]<out [ERROR : *]>, out com.airbnb.epoxy.preload.ViewMetadata?, out P>
//                          ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyModelPreloader#
//                                                                     ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/ViewMetadata#
//                                                                                        ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with().[P]
        ): EpoxyPreloader<P> =
//         ^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#
//                        ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with().[P]
            with(
                epoxyController,
//              ^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with().(epoxyController)
                requestHolderFactory,
//              ^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with().(requestHolderFactory)
                errorHandler,
//              ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with().(errorHandler)
                maxItemsToPreload,
//              ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with().(maxItemsToPreload)
                listOf(modelPreloader)
//              ^^^^^^ reference kotlin/collections/CollectionsKt#listOf().
//                     ^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with().(modelPreloader)
            )

        fun <P : PreloadRequestHolder> with(
//           ^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+1).[P]  <P : com.airbnb.epoxy.preload.PreloadRequestHolder>
//               ^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/PreloadRequestHolder#
//                                     ^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+1).  public final fun <P : com.airbnb.epoxy.preload.PreloadRequestHolder> with(epoxyController: [ERROR : EpoxyController], requestHolderFactory: () -> P, errorHandler: com.airbnb.epoxy.preload.PreloadErrorHandler /* = ([ERROR : Context], kotlin.RuntimeException /* = java.lang.RuntimeException */) -> kotlin.Unit */, maxItemsToPreload: kotlin.Int, modelPreloaders: kotlin.collections.List<com.airbnb.epoxy.preload.EpoxyModelPreloader<out [ERROR : EpoxyModel<*>]<out [ERROR : *]>, out com.airbnb.epoxy.preload.ViewMetadata?, out P>>): com.airbnb.epoxy.preload.EpoxyPreloader<P>
            epoxyController: EpoxyController,
//          ^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+1).(epoxyController)  value-parameter epoxyController: [ERROR : EpoxyController]
            requestHolderFactory: () -> P,
//          ^^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+1).(requestHolderFactory)  value-parameter requestHolderFactory: () -> P
//                                      ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+1).[P]
            errorHandler: PreloadErrorHandler,
//          ^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+1).(errorHandler)  value-parameter errorHandler: com.airbnb.epoxy.preload.PreloadErrorHandler /* = ([ERROR : Context], kotlin.RuntimeException /* = java.lang.RuntimeException */) -> kotlin.Unit */
//                        ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/PreloadErrorHandler#
            maxItemsToPreload: Int,
//          ^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+1).(maxItemsToPreload)  value-parameter maxItemsToPreload: kotlin.Int
//                             ^^^ reference kotlin/Int#
            modelPreloaders: List<EpoxyModelPreloader<out EpoxyModel<*>, out ViewMetadata?, out P>>
//          ^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+1).(modelPreloaders)  value-parameter modelPreloaders: kotlin.collections.List<com.airbnb.epoxy.preload.EpoxyModelPreloader<out [ERROR : EpoxyModel<*>]<out [ERROR : *]>, out com.airbnb.epoxy.preload.ViewMetadata?, out P>>
//                           ^^^^ reference kotlin/collections/List#
//                                ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyModelPreloader#
//                                                                           ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/ViewMetadata#
//                                                                                              ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+1).[P]
        ): EpoxyPreloader<P> {
//         ^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#
//                        ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+1).[P]

            return EpoxyPreloader(
                epoxyController,
//              ^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+1).(epoxyController)
                requestHolderFactory,
//              ^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+1).(requestHolderFactory)
                errorHandler,
//              ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+1).(errorHandler)
                maxItemsToPreload,
//              ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+1).(maxItemsToPreload)
                modelPreloaders
//              ^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+1).(modelPreloaders)
            )
        }

        /** Helper to create a preload scroll listener. Add the result to your RecyclerView. */
        fun <P : PreloadRequestHolder> with(
//           ^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+2).[P]  <P : com.airbnb.epoxy.preload.PreloadRequestHolder>
//               ^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/PreloadRequestHolder#
//                                     ^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+2).  public final fun <P : com.airbnb.epoxy.preload.PreloadRequestHolder> with(epoxyAdapter: [ERROR : EpoxyAdapter], requestHolderFactory: () -> P, errorHandler: com.airbnb.epoxy.preload.PreloadErrorHandler /* = ([ERROR : Context], kotlin.RuntimeException /* = java.lang.RuntimeException */) -> kotlin.Unit */, maxItemsToPreload: kotlin.Int, modelPreloaders: kotlin.collections.List<com.airbnb.epoxy.preload.EpoxyModelPreloader<out [ERROR : EpoxyModel<*>]<out [ERROR : *]>, out com.airbnb.epoxy.preload.ViewMetadata?, out P>>): com.airbnb.epoxy.preload.EpoxyPreloader<P>
            epoxyAdapter: EpoxyAdapter,
//          ^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+2).(epoxyAdapter)  value-parameter epoxyAdapter: [ERROR : EpoxyAdapter]
            requestHolderFactory: () -> P,
//          ^^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+2).(requestHolderFactory)  value-parameter requestHolderFactory: () -> P
//                                      ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+2).[P]
            errorHandler: PreloadErrorHandler,
//          ^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+2).(errorHandler)  value-parameter errorHandler: com.airbnb.epoxy.preload.PreloadErrorHandler /* = ([ERROR : Context], kotlin.RuntimeException /* = java.lang.RuntimeException */) -> kotlin.Unit */
//                        ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/PreloadErrorHandler#
            maxItemsToPreload: Int,
//          ^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+2).(maxItemsToPreload)  value-parameter maxItemsToPreload: kotlin.Int
//                             ^^^ reference kotlin/Int#
            modelPreloaders: List<EpoxyModelPreloader<out EpoxyModel<*>, out ViewMetadata?, out P>>
//          ^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+2).(modelPreloaders)  value-parameter modelPreloaders: kotlin.collections.List<com.airbnb.epoxy.preload.EpoxyModelPreloader<out [ERROR : EpoxyModel<*>]<out [ERROR : *]>, out com.airbnb.epoxy.preload.ViewMetadata?, out P>>
//                           ^^^^ reference kotlin/collections/List#
//                                ^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyModelPreloader#
//                                                                           ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/ViewMetadata#
//                                                                                              ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+2).[P]
        ): EpoxyPreloader<P> {
//         ^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#
//                        ^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+2).[P]

            return EpoxyPreloader(
                epoxyAdapter,
//              ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+2).(epoxyAdapter)
                requestHolderFactory,
//              ^^^^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+2).(requestHolderFactory)
                errorHandler,
//              ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+2).(errorHandler)
                maxItemsToPreload,
//              ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+2).(maxItemsToPreload)
                modelPreloaders
//              ^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloader#Companion#with(+2).(modelPreloaders)
            )
        }
    }
}

class EpoxyPreloadException(errorMessage: String) : RuntimeException(errorMessage)
//    ^^^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloadException#  public final class EpoxyPreloadException : kotlin.RuntimeException /* = java.lang.RuntimeException */
//    ^^^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloadException#`<init>`().  public constructor EpoxyPreloadException(errorMessage: kotlin.String)
//                          ^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloadException#`<init>`().(errorMessage)  value-parameter errorMessage: kotlin.String
//                                        ^^^^^^ reference kotlin/String#
//                                                  ^^^^^^^^^^^^^^^^ reference kotlin/RuntimeException#`<init>`(+1).
//                                                                   ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/EpoxyPreloadException#`<init>`().(errorMessage)

typealias PreloadErrorHandler = (Context, RuntimeException) -> Unit
//        ^^^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/PreloadErrorHandler#  public typealias PreloadErrorHandler = ([ERROR : Context], kotlin.RuntimeException) -> kotlin.Unit
//                               ^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloaderKt#`<no name provided>`.  val <no name provided>: kotlin.RuntimeException
//                                        ^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/EpoxyPreloaderKt#`<no name provided>`.  val <no name provided>: kotlin.RuntimeException
//                                        ^^^^^^^^^^^^^^^^ reference kotlin/RuntimeException#
//                                                             ^^^^ reference kotlin/Unit#

/**
 * Data about an image view to be preloaded. This data is used to construct a Glide image request.
 *
 * @param metadata Any custom, additional data that the [EpoxyModelPreloader] chooses to provide that may be necessary to create the image request.
 */
class ViewData<out U : ViewMetadata?>(
//    ^^^^^^^^ definition com/airbnb/epoxy/preload/ViewData#  public final class ViewData<out U : com.airbnb.epoxy.preload.ViewMetadata?>
//    ^^^^^^^^ definition com/airbnb/epoxy/preload/ViewData#`<init>`().  public constructor ViewData<out U : com.airbnb.epoxy.preload.ViewMetadata?>(viewId: kotlin.Int, width: kotlin.Int, height: kotlin.Int, metadata: U)
//                 ^ definition com/airbnb/epoxy/preload/ViewData#[U]  <out U : com.airbnb.epoxy.preload.ViewMetadata?>
//                     ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/ViewMetadata#
    @IdRes val viewId: Int,
//   ^^^^^ reference androidx/annotation/IdRes#`<init>`().
//             ^^^^^^ definition com/airbnb/epoxy/preload/ViewData#viewId.  public final val viewId: kotlin.Int
//             ^^^^^^ definition com/airbnb/epoxy/preload/ViewData#getViewId().  public final val viewId: kotlin.Int
//             ^^^^^^ definition com/airbnb/epoxy/preload/ViewData#`<init>`().(viewId)  value-parameter viewId: kotlin.Int
//                     ^^^ reference kotlin/Int#
    @Px val width: Int,
//   ^^ reference androidx/annotation/Px#`<init>`().
//          ^^^^^ definition com/airbnb/epoxy/preload/ViewData#width.  public final val width: kotlin.Int
//          ^^^^^ definition com/airbnb/epoxy/preload/ViewData#getWidth().  public final val width: kotlin.Int
//          ^^^^^ definition com/airbnb/epoxy/preload/ViewData#`<init>`().(width)  value-parameter width: kotlin.Int
//                 ^^^ reference kotlin/Int#
    @Px val height: Int,
//   ^^ reference androidx/annotation/Px#`<init>`().
//          ^^^^^^ definition com/airbnb/epoxy/preload/ViewData#height.  public final val height: kotlin.Int
//          ^^^^^^ definition com/airbnb/epoxy/preload/ViewData#getHeight().  public final val height: kotlin.Int
//          ^^^^^^ definition com/airbnb/epoxy/preload/ViewData#`<init>`().(height)  value-parameter height: kotlin.Int
//                  ^^^ reference kotlin/Int#
    val metadata: U
//      ^^^^^^^^ definition com/airbnb/epoxy/preload/ViewData#metadata.  public final val metadata: U
//      ^^^^^^^^ definition com/airbnb/epoxy/preload/ViewData#getMetadata().  public final val metadata: U
//      ^^^^^^^^ definition com/airbnb/epoxy/preload/ViewData#`<init>`().(metadata)  value-parameter metadata: U
//                ^ reference com/airbnb/epoxy/preload/ViewData#[U]
)

interface ViewMetadata {
//        ^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/ViewMetadata#  public interface ViewMetadata
    companion object {
//            ^^^^^^^^^ definition com/airbnb/epoxy/preload/ViewMetadata#Companion#  public companion object
        fun getDefault(view: View): ViewMetadata? {
//          ^^^^^^^^^^ definition com/airbnb/epoxy/preload/ViewMetadata#Companion#getDefault().  public final fun getDefault(view: [ERROR : View]): com.airbnb.epoxy.preload.ViewMetadata?
//                     ^^^^ definition com/airbnb/epoxy/preload/ViewMetadata#Companion#getDefault().(view)  value-parameter view: [ERROR : View]
//                                  ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/ViewMetadata#
            return when (view) {
//                       ^^^^ reference com/airbnb/epoxy/preload/ViewMetadata#Companion#getDefault().(view)
                is ImageView -> ImageViewMetadata(view.scaleType)
//                              ^^^^^^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/ImageViewMetadata#`<init>`().
//                                                ^^^^ reference com/airbnb/epoxy/preload/ViewMetadata#Companion#getDefault().(view)
                else -> null
            }
        }
    }
}

/**
 * Default implementation of [ViewMetadata] for an ImageView.
 * This data can help the preload request know how to configure itself.
 */
open class ImageViewMetadata(
//         ^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/ImageViewMetadata#  public open class ImageViewMetadata : com.airbnb.epoxy.preload.ViewMetadata
//         ^^^^^^^^^^^^^^^^^ definition com/airbnb/epoxy/preload/ImageViewMetadata#`<init>`().  public constructor ImageViewMetadata(scaleType: [ERROR : ImageView.ScaleType])
    val scaleType: ImageView.ScaleType
//      ^^^^^^^^^ definition com/airbnb/epoxy/preload/ImageViewMetadata#scaleType.  public final val scaleType: [ERROR : ImageView.ScaleType]
//      ^^^^^^^^^ definition com/airbnb/epoxy/preload/ImageViewMetadata#getScaleType().  public final val scaleType: [ERROR : ImageView.ScaleType]
//      ^^^^^^^^^ definition com/airbnb/epoxy/preload/ImageViewMetadata#`<init>`().(scaleType)  value-parameter scaleType: [ERROR : ImageView.ScaleType]
) : ViewMetadata
//  ^^^^^^^^^^^^ reference com/airbnb/epoxy/preload/ViewMetadata#